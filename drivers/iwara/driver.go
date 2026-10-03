package iwara

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func (d *IwaraZip) Config() driver.Config { return config }

func (d *IwaraZip) GetAddition() driver.Additional { return &d.Addition }

func (d *IwaraZip) Init(ctx context.Context) error {
	if strings.TrimSpace(d.Endpoint) == "" {
		d.Endpoint = "https://www.iwara.zip"
	}
	d.Endpoint = strings.TrimRight(d.Endpoint, "/")
	if d.APIKey1 == "" || d.APIKey2 == "" {
		return errors.New("api_key_1 and api_key_2 (iwara.zip API keys) are required")
	}
	return nil
}

func (d *IwaraZip) Drop(ctx context.Context) error {
	d.tokenMu.Lock()
	d.accessToken, d.accountID, d.tokenUsed = "", "", time.Time{}
	d.tokenMu.Unlock()
	return nil
}

func (d *IwaraZip) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.list(ctx, dir)
}

func (d *IwaraZip) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return d.link(ctx, file)
}

func (d *IwaraZip) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	return d.makeDir(ctx, parentDir, dirName)
}

func (d *IwaraZip) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.move(ctx, srcObj, dstDir)
}

func (d *IwaraZip) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	return d.rename(ctx, srcObj, newName)
}

func (d *IwaraZip) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.copy(ctx, srcObj, dstDir)
}

func (d *IwaraZip) Remove(ctx context.Context, obj model.Obj) error {
	return d.remove(ctx, obj)
}

func (d *IwaraZip) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	return d.put(ctx, dstDir, stream, up)
}

var _ driver.Driver = (*IwaraZip)(nil)
