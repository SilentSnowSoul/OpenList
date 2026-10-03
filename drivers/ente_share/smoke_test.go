package ente_share

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/ente"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

// TestEnteShareSmoke exercises the share driver against the real Ente API. It
// is skipped unless ENTE_SMOKE_SHARE_URL is set, so `go test ./...` stays
// offline and CI never runs it:
//
//	ENTE_SMOKE_SHARE_URL='https://share.ente.io/c/<token>#<key>' \
//	  go test ./drivers/ente_share/ -run TestEnteShareSmoke -v
//
// Optional: ENTE_SMOKE_ENDPOINT (default https://api.ente.com).
func TestEnteShareSmoke(t *testing.T) {
	shareURL := os.Getenv("ENTE_SMOKE_SHARE_URL")
	if shareURL == "" {
		t.Skip("ENTE_SMOKE_SHARE_URL not set; skipping real-network smoke test")
	}
	ctx := context.Background()
	d := &EnteShare{Addition: Addition{ShareURL: shareURL, Endpoint: ente.DefaultEndpoint}}
	if e := os.Getenv("ENTE_SMOKE_ENDPOINT"); e != "" {
		d.Endpoint = e
	}
	d.SetStorage(model.Storage{MountPath: "/ente_smoke"})
	if err := d.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	objs, err := d.List(ctx, &model.Object{Path: d.GetRootPath()}, model.ListArgs{ReqPath: "/"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) == 0 {
		t.Fatal("list returned no objects")
	}
	t.Logf("listed %d objects", len(objs))

	checked := 0
	for _, o := range objs {
		if o.IsDir() {
			continue
		}
		smokeLink(t, ctx, d, o, model.LinkArgs{})
		checked++
		if checked == 3 {
			break
		}
	}
}

func smokeLink(t *testing.T, ctx context.Context, d interface {
	Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error)
}, o model.Obj, args model.LinkArgs) {
	t.Helper()
	link, err := d.Link(ctx, o, args)
	if err != nil {
		t.Fatalf("link %s: %v", o.GetName(), err)
	}
	if link.ContentLength != o.GetSize() {
		t.Errorf("%s: content length = %d, want %d", o.GetName(), link.ContentLength, o.GetSize())
	}
	rc, err := link.RangeReader.RangeRead(ctx, http_range.Range{Start: 0, Length: -1})
	if err != nil {
		t.Fatalf("open %s: %v", o.GetName(), err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("read %s: %v", o.GetName(), err)
	}
	if n != link.ContentLength {
		t.Errorf("%s: read %d bytes, want %d", o.GetName(), n, link.ContentLength)
	}
	t.Logf("%s: %d bytes ok", o.GetName(), n)

	thumb, ok := model.GetThumb(o)
	if !ok || thumb == "" {
		return
	}
	tl, err := d.Link(ctx, o, model.LinkArgs{Type: "thumb"})
	if err != nil {
		t.Fatalf("thumb link %s: %v", o.GetName(), err)
	}
	trc, err := tl.RangeReader.RangeRead(ctx, http_range.Range{Start: 0, Length: -1})
	if err != nil {
		t.Fatalf("open thumb %s: %v", o.GetName(), err)
	}
	defer trc.Close()
	tn, err := io.Copy(io.Discard, trc)
	if err != nil {
		t.Fatalf("read thumb %s: %v", o.GetName(), err)
	}
	if tn != tl.ContentLength {
		t.Errorf("%s: read %d thumbnail bytes, want %d", o.GetName(), tn, tl.ContentLength)
	}
	t.Logf("%s: thumbnail %d bytes ok", o.GetName(), tn)
}
