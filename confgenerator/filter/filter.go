// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:generate gocc -a -o internal/generated internal/filter.bnf
// To install gocc: go get github.com/goccmack/gocc

package filter

import (
	"fmt"
	"strings"

	"github.com/GoogleCloudPlatform/ops-agent/confgenerator/filter/internal/ast"
	"github.com/GoogleCloudPlatform/ops-agent/confgenerator/filter/internal/generated/lexer"
	"github.com/GoogleCloudPlatform/ops-agent/confgenerator/filter/internal/generated/parser"
	"github.com/GoogleCloudPlatform/ops-agent/confgenerator/fluentbit"
)

type Member struct {
	ast.Target
}

func NewMember(m string) (*Member, error) {
	lex := lexer.NewLexer([]byte(m))
	p := parser.NewParser()
	out, err := p.Parse(lex)
	if err != nil {
		return nil, err
	}
	r, ok := out.(ast.Restriction)
	if !ok || r.Operator != "GLOBAL" {
		return nil, fmt.Errorf("not a field: %#v", out)
	}
	return &Member{r.LHS}, nil
}

var LuaQuote = ast.LuaQuote

type Filter struct {
	expr ast.Expression
}

func NewFilter(f string) (*Filter, error) {
	lex := lexer.NewLexer([]byte(f))
	p := parser.NewParser()
	out, err := p.Parse(lex)
	if err != nil {
		return nil, err
	}
	if out, ok := out.(ast.Expression); ok {
		return &Filter{out}, nil
	}
	return nil, fmt.Errorf("not an expression: %+v", out)
}

// FluentConfig returns components that are intended to be positioned between corresponding nest/lift filters and a Lua expression to evaluate.
func (f *Filter) innerFluentConfig(tag, prefix string) ([]fluentbit.Component, string) {
	return f.expr.FluentConfig(tag, prefix)
}

// MatchesAny returns a single Filter that matches if any of the child filters match.
func MatchesAny(filters []*Filter) *Filter {
	d := ast.Disjunction{}
	for _, f := range filters {
		d = append(d, f.expr)
	}
	return &Filter{expr: d}
}

// AllFluentConfig returns components (if any) and Lua code that sets a Boolean local variable for each filter to indicate if that filter matched.
func AllFluentConfig(tag string, filters map[string]*Filter) ([]fluentbit.Component, string) {
	var c []fluentbit.Component
	var lua strings.Builder
	var vars []string
	for k := range filters {
		vars = append(vars, k)
	}

	for i, k := range vars {
		prefix := fmt.Sprintf("__match_%d", i)
		filter := filters[k]
		filterComponents, filterExpr := filter.innerFluentConfig(tag, prefix)
		c = append(c, filterComponents...)
		lua.WriteString(fmt.Sprintf("local %s = %s\n", k, filterExpr))
	}
	if len(c) == 0 {
		// If we didn't need any filters, just return the Lua code.
		return nil, lua.String()
	}
	out := []fluentbit.Component{{
		Kind: "FILTER",
		Config: map[string]string{
			"Name":       "nest",
			"Match":      tag,
			"Operation":  "nest",
			"Nest_under": "record",
			"Wildcard":   "*",
		},
	}}
	out = append(out, c...)
	out = append(out, fluentbit.Component{
		Kind: "FILTER",
		Config: map[string]string{
			"Name":         "nest",
			"Match":        tag,
			"Operation":    "lift",
			"Nested_under": "record",
		},
	})
	// Remove match keys
	lua.WriteString(`
for k, v in pairs(record) do
  if string.match(k, "^__match_.+") then
    record[k] = nil
  end
end
`)
	return out, lua.String()
}

// innerComponents returns only the logical modify filters that are intended to be
// positioned between corresponding nest/grep/lift filters.
func (f *Filter) innerComponents(tag string) []fluentbit.Component {
	match := fmt.Sprintf("__match_%s", strings.ReplaceAll(tag, ".", "_"))
	return f.expr.Components(tag, match)
}

func (f *Filter) Components(tag string, isExclusionFilter bool) []fluentbit.Component {
	return AllComponents(tag, []*Filter{f}, isExclusionFilter)
}

// AllComponents returns a list of FluentBit components for a list of filters.
// As an optimization, only a single set of nest/grep/lift components is
// emitted in total.
func AllComponents(tag string, filters []*Filter, isExclusionFilter bool) []fluentbit.Component {
	var parity string
	if isExclusionFilter {
		parity = "Exclude"
	} else {
		parity = "Regex"
	}
	// TODO: Re-implement using Lua once regex is supported. Lua has been shown to perform better
	// than the next/modify/grep/lift pattern used here, but we are unable to use Lua for now since
	// it does not yet support regex.
	c := []fluentbit.Component{{
		Kind: "FILTER",
		Config: map[string]string{
			"Name":       "nest",
			"Match":      tag,
			"Operation":  "nest",
			"Nest_under": "record",
			"Wildcard":   "*",
		},
	}}
	match := fmt.Sprintf("__match_%s", strings.ReplaceAll(tag, ".", "_"))
	for _, filter := range filters {
		c = append(c, filter.innerComponents(tag)...)
	}
	c = append(c,
		fluentbit.Component{
			Kind: "FILTER",
			Config: map[string]string{
				"Name":  "grep",
				"Match": tag,
				parity:  fmt.Sprintf("%s 1", match),
			},
		},
		fluentbit.Component{
			Kind: "FILTER",
			Config: map[string]string{
				"Name":            "modify",
				"Match":           tag,
				"Remove_wildcard": match,
			},
		},
		fluentbit.Component{
			Kind: "FILTER",
			Config: map[string]string{
				"Name":         "nest",
				"Match":        tag,
				"Operation":    "lift",
				"Nested_under": "record",
			},
		},
	)
	return c
}
