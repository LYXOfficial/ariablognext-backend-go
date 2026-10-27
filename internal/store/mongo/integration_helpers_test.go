//go:build integration

package mongo

import "encoding/json"

func jsonUnmarshalRaw(raw []json.RawMessage, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}
