package reversebin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// DetectorOutput is the JSON object a dynamic proxy detector writes to stdout.
//
// All fields are optional. When present, they override static reverse-bin
// configuration for the request being served.
type DetectorOutput struct {
	Executable       *[]string `json:"executable,omitempty" jsonschema:"minItems=1" jsonschema_description:"Backend command and arguments to launch on demand."`
	WorkingDirectory *string   `json:"working_directory,omitempty" jsonschema_description:"Directory where the backend command runs."`
	Envs             *[]string `json:"envs,omitempty" jsonschema_description:"Environment entries passed to the backend in KEY=value form."`
	ReverseProxyTo   *string   `json:"reverse_proxy_to,omitempty" jsonschema_description:"Upstream address to proxy to, such as 127.0.0.1:8080 or unix//tmp/app.sock."`
	HealthMethod     *string   `json:"health_method,omitempty" jsonschema_description:"HTTP method used for readiness checks on non-Unix upstreams."`
	HealthPath       *string   `json:"health_path,omitempty" jsonschema_description:"HTTP path used for readiness checks on non-Unix upstreams."`
	HealthStatus     *int      `json:"health_status,omitempty" jsonschema:"minimum=100,maximum=599" jsonschema_description:"Exact HTTP status code expected from readiness checks."`
}

func parseDetectorOutput(data []byte) (*DetectorOutput, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var output DetectorOutput
	if err := dec.Decode(&output); err != nil {
		return nil, fmt.Errorf("invalid detector output: %w", err)
	}
	if err := dec.Decode(new(struct{})); err != io.EOF {
		return nil, fmt.Errorf("invalid detector output: must contain exactly one JSON object")
	}
	if err := validateDetectorOutput(output); err != nil {
		return nil, fmt.Errorf("invalid detector output: %w", err)
	}
	return &output, nil
}

func validateDetectorOutput(output DetectorOutput) error {
	if output.Executable != nil {
		if len(*output.Executable) == 0 {
			return fmt.Errorf("executable must not be empty when provided")
		}
		for i, arg := range *output.Executable {
			if arg == "" {
				return fmt.Errorf("executable[%d] must not be empty", i)
			}
		}
	}
	if output.WorkingDirectory != nil && strings.TrimSpace(*output.WorkingDirectory) == "" {
		return fmt.Errorf("working_directory must not be empty when provided")
	}
	if output.Envs != nil {
		for i, env := range *output.Envs {
			name, _, ok := strings.Cut(env, "=")
			if !ok || name == "" {
				return fmt.Errorf("envs[%d] must be in KEY=value form", i)
			}
		}
	}
	if output.ReverseProxyTo != nil && strings.TrimSpace(*output.ReverseProxyTo) == "" {
		return fmt.Errorf("reverse_proxy_to must not be empty when provided")
	}
	if output.HealthMethod != nil && strings.TrimSpace(*output.HealthMethod) == "" {
		return fmt.Errorf("health_method must not be empty when provided")
	}
	if output.HealthPath != nil && !strings.HasPrefix(*output.HealthPath, "/") {
		return fmt.Errorf("health_path must start with /")
	}
	if output.HealthStatus != nil && (*output.HealthStatus < 100 || *output.HealthStatus > 599) {
		return fmt.Errorf("health_status must be between 100 and 599")
	}
	return nil
}
