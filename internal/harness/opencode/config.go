package opencode

import (
	"encoding/json"
	"fmt"
)

// The same adapter runs implementation, review and repair work orders. The
// prompt supplies the role; this profile only makes execution unattended.
const profile = `{
  "description": "Execute work orders supplied by Ploopy, unattended.",
  "mode": "primary",
  "permission": {
    "question": "deny",
    "plan_enter": "deny",
    "plan_exit": "deny"
  }
}`

func sessionConfig(inherited string) (string, error) {
	config := make(map[string]json.RawMessage)
	if inherited != "" {
		if err := json.Unmarshal([]byte(inherited), &config); err != nil || config == nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	agents := make(map[string]json.RawMessage)
	if raw, ok := config["agent"]; ok {
		if err := json.Unmarshal(raw, &agents); err != nil || agents == nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT agent must be a JSON object")
		}
	}
	agents["ploopy"] = json.RawMessage(profile)
	config["agent"], _ = json.Marshal(agents)
	data, err := json.Marshal(config)
	return string(data), err
}
