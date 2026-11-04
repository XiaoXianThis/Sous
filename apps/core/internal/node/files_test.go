package node

import (
	"encoding/json"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	in := `{
  // a comment
  "url": "http://x//y", /* block */
  "list": [1, 2,],
  "s": "a \" // not a comment",
}`
	var v map[string]any
	if err := json.Unmarshal(StripJSONC([]byte(in)), &v); err != nil {
		t.Fatalf("%v: %s", err, StripJSONC([]byte(in)))
	}
	if v["url"] != "http://x//y" || v["s"] != `a " // not a comment` {
		t.Fatalf("got %v", v)
	}
}
