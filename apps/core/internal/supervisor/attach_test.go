package supervisor

import (
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/attach"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestUserFilesReachTheModel(t *testing.T) {
	files := attach.Open(t.TempDir())
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	img, err := files.Save(strings.NewReader(string(png)), "shot.png")
	if err != nil || img.Mime != "image/png" {
		t.Fatalf("image: %+v %v", img, err)
	}
	txt, _ := files.Save(strings.NewReader("第一行\n第二行"), "a.txt")
	bin, _ := files.Save(strings.NewReader("\x00\x01\x02\xff\xfe"), "x.bin")

	msgs := []store.Message{{ID: "1", Role: "user", Seq: 1, Content: "看看这些", Attachments: []event.Attachment{img, txt, bin, {ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Name: "gone.txt", Mime: "text/plain"}}}}
	out := toProviderWith(files, msgs)
	if len(out) != 1 {
		t.Fatalf("messages: %+v", out)
	}
	m := out[0]
	if m.Parts[0].Text != "看看这些" {
		t.Errorf("the text must stay the first part (the mock reads only it): %+v", m.Parts[0])
	}
	var images int
	var text strings.Builder
	for _, p := range m.Parts {
		if p.Image != nil {
			images++
			if p.Image.Mime != "image/png" {
				t.Errorf("mime %q", p.Image.Mime)
			}
		}
		text.WriteString(p.Text)
	}
	got := text.String()
	if images != 1 || !strings.Contains(got, "第二行") || !strings.Contains(got, "x.bin") || !strings.Contains(got, "gone.txt 已经找不到了") {
		t.Errorf("images %d, text %q", images, got)
	}

	// Without the store the model only hears about the files.
	bare := toProvider(msgs)[0]
	if len(bare.Parts) != 5 || bare.Parts[1].Image != nil || !strings.Contains(bare.Parts[1].Text, "shot.png") {
		t.Errorf("bare: %+v", bare.Parts)
	}

	// Summaries and token counts see the names.
	if c := plain(clip(msgs[0])).Content; !strings.Contains(c, "【附件：shot.png、a.txt、x.bin、gone.txt】") {
		t.Errorf("plain: %q", c)
	}
}
