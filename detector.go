package reversebin

import "github.com/tarasglek/caddy-reverse-bin/detectorschema"

// DetectorOutput is the JSON object a dynamic proxy detector writes to stdout.
type DetectorOutput = detectorschema.DetectorOutput

func parseDetectorOutput(data []byte) (*DetectorOutput, error) {
	return detectorschema.Parse(data)
}

func validateDetectorOutput(output DetectorOutput) error {
	return detectorschema.Validate(output)
}
