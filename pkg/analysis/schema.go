package analysis

import "embed"

// SchemaV1 holds the v1 contract's JSON Schemas: results.schema.json,
// power-request.schema.json and power-result.schema.json.
//
//go:embed schema/v1/*.json
var SchemaV1 embed.FS
