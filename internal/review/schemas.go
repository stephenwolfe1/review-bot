package review

import (
	"bytes"
	"encoding/json"
)

// JSON schemas for the model's replies. Ollama constrains generation to them,
// so a reply always has the right shape and ends when the object is complete.
// They match the formats described in the prompts.
//
// Properties keep their order: constrained output follows the schema's key
// order, and the model should say where a problem is before explaining it.

var severityEnum = []string{"critical", "high", "medium", "low"}

var findingsSchema = obj(
	prop{"findings", map[string]any{
		"type":     "array",
		"maxItems": 20,
		"items": obj(
			prop{"line", typ("integer")},
			prop{"end_line", typ("integer")},
			prop{"severity", enum(severityEnum...)},
			prop{"title", typ("string")},
			prop{"body", typ("string")},
			prop{"example", typ("string")},
			prop{"suggestion", typ("string")},
			prop{"confidence", typ("number")},
		),
	}},
)

// The analysis comes first, so the model traces the code before it commits
// to a verdict.
var verifySchema = obj(
	prop{"analysis", typ("string")},
	prop{"valid", typ("boolean")},
	prop{"suggestion_ok", typ("boolean")},
	prop{"severity", enum(severityEnum...)},
)

var resolveSchema = obj(
	prop{"status", enum("fixed", "not_fixed", "unclear")},
	prop{"reason", typ("string")},
)

type prop struct {
	name   string
	schema any
}

// props marshals as a JSON object with its keys in order.
type props []prop

func (ps props) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p.name)
		v, err := json.Marshal(p.schema)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// obj is an object schema in which every property is required.
func obj(ps ...prop) map[string]any {
	required := make([]string, len(ps))
	for i, p := range ps {
		required[i] = p.name
	}
	return map[string]any{"type": "object", "properties": props(ps), "required": required}
}

func typ(t string) map[string]any { return map[string]any{"type": t} }

func enum(vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals}
}
