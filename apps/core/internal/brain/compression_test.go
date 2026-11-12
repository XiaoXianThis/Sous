package brain

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
)

func TestResponseCompressionNegotiation(t *testing.T) {
	data := bytes.Repeat([]byte(`{"status":"succeeded","summary":"completed"},`), 1000)
	for _, accepted := range []bool{false, true} {
		req := tunnel.RPCMsg{Header: map[string]string{}}
		if accepted {
			req.Header["X-Sous-Accept-Encoding"] = "gzip"
		}
		res := tunnel.RPCMsg{Body: trust.B64.EncodeToString(data), Header: map[string]string{"Content-Type": "application/json"}}
		compressResponse(req, &res, data)
		wire, err := trust.B64.DecodeString(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if accepted {
			if res.Header["X-Sous-Content-Encoding"] != "gzip" || len(wire) >= len(data)/2 {
				t.Fatal("large reply was not compressed")
			}
			z, err := gzip.NewReader(bytes.NewReader(wire))
			if err != nil {
				t.Fatal(err)
			}
			wire, err = io.ReadAll(z)
			z.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else if res.Header["X-Sous-Content-Encoding"] != "" {
			t.Fatal("old client received compressed body")
		}
		if !bytes.Equal(wire, data) {
			t.Fatal("reply changed")
		}
	}
}
