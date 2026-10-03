package ente

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
)

func smokeEndpoint() string {
	if e := os.Getenv("ENTE_SMOKE_ENDPOINT"); e != "" {
		return e
	}
	return DefaultEndpoint
}

// smokeLink drives one full download and, when the object has one, one
// thumbnail through the same code path the proxy uses.
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

// TestEnteAccountSmoke exercises the account driver against the real Ente API.
// It is skipped unless ENTE_SMOKE_TOKEN and ENTE_SMOKE_MASTER_KEY are set, so
// `go test ./...` stays offline and CI never runs it:
//
//	ENTE_SMOKE_TOKEN=<token> ENTE_SMOKE_MASTER_KEY=<b64> \
//	  go test ./drivers/ente/ -run TestEnteAccountSmoke -v
//
// Optional: ENTE_SMOKE_ENDPOINT (default https://api.ente.com),
// ENTE_SMOKE_SECRET_KEY (needed to list shared albums),
// ENTE_SMOKE_SHOW_HIDDEN=true.
func TestEnteAccountSmoke(t *testing.T) {
	token := os.Getenv("ENTE_SMOKE_TOKEN")
	masterKey := os.Getenv("ENTE_SMOKE_MASTER_KEY")
	if token == "" || masterKey == "" {
		t.Skip("ENTE_SMOKE_TOKEN / ENTE_SMOKE_MASTER_KEY not set; skipping real-network smoke test")
	}
	ctx := context.Background()
	d := &Ente{Addition: Addition{
		Token:      token,
		MasterKey:  masterKey,
		SecretKey:  os.Getenv("ENTE_SMOKE_SECRET_KEY"),
		Endpoint:   smokeEndpoint(),
		ShowHidden: os.Getenv("ENTE_SMOKE_SHOW_HIDDEN") == "true",
	}}
	d.SetStorage(model.Storage{MountPath: "/ente_smoke"})
	if err := d.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	albums, err := d.List(ctx, &model.Object{Path: d.GetRootPath()}, model.ListArgs{ReqPath: "/"})
	if err != nil {
		t.Fatalf("list root: %v", err)
	}
	if len(albums) == 0 {
		t.Fatal("no collections visible; check the credentials")
	}
	t.Logf("listed %d collections", len(albums))

	album := albums[0]
	files, err := d.List(ctx, album, model.ListArgs{ReqPath: "/" + album.GetName()})
	if err != nil {
		t.Fatalf("list %s: %v", album.GetName(), err)
	}
	if len(files) == 0 {
		t.Fatalf("collection %s is empty", album.GetName())
	}
	t.Logf("collection %q holds %d files", album.GetName(), len(files))

	smokeLink(t, ctx, d, files[0], model.LinkArgs{})
}
