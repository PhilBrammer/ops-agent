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

// Package confgenerator provides functions to generate subagents configuration from unified agent.
package confgenerator

import (
	"fmt"
	"os"
	"path/filepath"

	yaml "github.com/goccy/go-yaml"
)

// MergeConfFiles merges the user provided config with the built-in config struct for the platform.
// It will write the merged config file to disk and return the config bytes.
func MergeConfFiles(userConfPath, confDebugFolder, platform string, builtInConfStructs map[string]*UnifiedConfig) ([]byte, error) {
	builtInConfPath := filepath.Join(confDebugFolder, "built-in-config.yaml")
	mergedConfPath := filepath.Join(confDebugFolder, "merged-config.yaml")

	// Write the built-in conf to disk for debugging purpose.
	builtInStruct := builtInConfStructs[platform]
	builtInYaml, err := yaml.Marshal(builtInStruct)
	if err != nil {
		return nil, fmt.Errorf("failed to convert the built-in config %q to yaml: %w \n", builtInConfPath, err)
	}

	if err := writeConfigFile(builtInYaml, builtInConfPath); err != nil {
		return nil, err
	}

	mergedConf, err := mergeConfFiles(builtInConfPath, userConfPath, mergedConfPath, platform, builtInConfStructs)
	if err != nil {
		return nil, err
	}

	mergedConfigYaml, err := yaml.Marshal(mergedConf)
	if err != nil {
		return nil, fmt.Errorf("failed to convert the merged config %q to yaml: %w \n", mergedConfPath, err)
	}
	if err := writeConfigFile(mergedConfigYaml, mergedConfPath); err != nil {
		return nil, err
	}

	return mergedConfigYaml, nil
}

func mergeConfFiles(builtInConfPath, userConfPath, mergedConfPath, platform string, builtInConfStructs map[string]*UnifiedConfig) (*UnifiedConfig, error) {
	builtInStruct := builtInConfStructs[platform]

	// Read the built-in config file.
	original, err := builtInStruct.DeepCopy(platform)
	if err != nil {
		return nil, err
	}

	// Optionally merge the user config file.
	if _, err = os.Stat(userConfPath); err != nil {
		if os.IsNotExist(err) {
			// Skip the merge if the user config file does not exist.
		} else {
			return nil, fmt.Errorf("failed to retrieve the user config file %q: %w \n", userConfPath, err)
		}
	} else {
		overrides, err := ReadUnifiedConfigFromFile(userConfPath, platform)
		if err != nil {
			return nil, err
		}
		mergeConfigs(&original, &overrides)
	}

	return &original, nil
}

func mergeConfigs(original, overrides *UnifiedConfig) {
	// For "default_pipeline", we go one level deeper.
	// this covers 2 cases:
	// 1. if "<receivers / processors / exporters>: []" is specified explicitly in user config, the entity gets cleared.
	// 2. if "<receivers / processors / exporters>" is a non-empty list, it overrides the built-in list.
	//    e.g. users might use the config below to turn off iis metrics on windows.
	//    metrics:
	//      service:
	//        pipelines:
	//          default_pipeline:
	//            receivers: [hostmetrics,mssql]
	if overrides.Logging != nil {
		// Overrides logging.receivers.
		for k, v := range overrides.Logging.Receivers {
			original.Logging.Receivers[k] = v
		}

		// Overrides logging.processors.
		original.Logging.Processors = map[string]LoggingProcessor{}
		for k, v := range overrides.Logging.Processors {
			original.Logging.Processors[k] = v
		}
		// Skip deprecated logging.exporters.
		// Override logging.service.pipelines
		if overrides.Logging.Service != nil {
			if overrides.Logging.Service.LogLevel != "info" {
				original.Logging.Service.LogLevel = overrides.Logging.Service.LogLevel
			}
			for name, pipeline := range overrides.Logging.Service.Pipelines {
				// skips logging.service.pipelines.*.exporters
				pipeline.ExporterIDs = nil
				if name == "default_pipeline" {
					// overrides logging.service.pipelines.default_pipeline.receivers
					if ids := pipeline.ReceiverIDs; ids != nil {
						original.Logging.Service.Pipelines["default_pipeline"].ReceiverIDs = ids
					}

					// overrides logging.service.pipelines.default_pipeline.processors
					if ids := pipeline.ProcessorIDs; ids != nil {
						original.Logging.Service.Pipelines["default_pipeline"].ProcessorIDs = ids
					}
				} else {
					// Overrides logging.service.pipelines.<non_default_pipelines>
					original.Logging.Service.Pipelines[name] = pipeline
				}
			}
		}
	}
	if overrides.Metrics != nil {
		// Overrides metrics.receivers.
		for k, v := range overrides.Metrics.Receivers {
			original.Metrics.Receivers[k] = v
		}

		// Overrides metrics.processors.
		for k, v := range overrides.Metrics.Processors {
			original.Metrics.Processors[k] = v
		}

		if overrides.Metrics.Service != nil {
			if overrides.Metrics.Service.LogLevel != "info" {
				original.Metrics.Service.LogLevel = overrides.Metrics.Service.LogLevel
			}
			for name, pipeline := range overrides.Metrics.Service.Pipelines {
				// skips metrics.service.pipelines.*.exporters
				pipeline.ExporterIDs = nil
				// Overrides metrics.service.pipelines.*
				original.Metrics.Service.Pipelines[name] = pipeline
			}
		}
	}
}
