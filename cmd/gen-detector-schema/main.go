package main

import (
	"encoding/json"
	"fmt"
	"os"

	jsonschema "github.com/invopop/jsonschema"
	reversebin "github.com/tarasglek/caddy-reverse-bin"
)

func main() {
	reflector := jsonschema.Reflector{
		BaseSchemaID:               jsonschema.ID("https://github.com/tarasglek/caddy-reverse-bin/schemas/"),
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: true,
		AllowAdditionalProperties:  false,
	}

	schema := reflector.Reflect(&reversebin.DetectorOutput{})

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(schema); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode detector schema: %v\n", err)
		os.Exit(1)
	}
}
