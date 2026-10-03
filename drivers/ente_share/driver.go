package ente_share

import (
	"context"
	"fmt"
	stdpath "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/ente"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"
)

var _ driver.Driver = (*EnteShare)(nil)

// EnteShare mounts a single public Ente share: the root directory lists every
// file of the shared album.
type EnteShare struct {
	model.Storage
	Addition

	client             *ente.Client
	collectionKey      []byte
	mu                 sync.Mutex
	pendingDeviceToken string
	// saveStorage persists the addition once a new linkDeviceToken arrives. It is
	// a field so tests can exercise the driver without touching the database.
	saveStorage func()
}

func (d *EnteShare) Config() driver.Config {
	return config
}

func (d *EnteShare) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *EnteShare) Init(_ context.Context) error {
	token, key, err := ente.ParseShareURL(d.ShareURL)
	if err != nil {
		return err
	}
	d.collectionKey = key
	d.client = ente.NewClient(d.Endpoint, token, ente.AuthAccessHeader,
		ente.WithDeviceToken(d.DeviceToken, func(t string) {
			d.mu.Lock()
			defer d.mu.Unlock()
			// A token that was never consumed was never persisted: the device slot
			// may be occupied twice.
			if d.pendingDeviceToken != "" {
				log.Warn("[EnteShare] linkDeviceToken 未持久化,设备位可能被重复占用")
			}
			d.pendingDeviceToken = t
		}))
	d.saveStorage = func() { op.MustSaveDriverStorage(d) }
	return nil
}

func (d *EnteShare) Drop(_ context.Context) error {
	return nil
}

func (d *EnteShare) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	defer d.flushPendingDeviceToken()
	if dir.GetPath() != d.GetRootPath() {
		// A share has a single flat album: nothing below the root is listable.
		return nil, nil
	}
	return d.listFiles(ctx, dir)
}

func (d *EnteShare) Get(ctx context.Context, path string) (model.Obj, error) {
	defer d.flushPendingDeviceToken()
	path = utils.FixAndCleanPath(path)
	// op.Get resolves "/" through IRootPath, so this is a defensive fallback.
	if path == "/" {
		return ente.RootObj(d.GetRootPath(), d.GetStorage().Modified), nil
	}
	name := strings.Trim(path, "/")
	if name == "" || strings.Contains(name, "/") {
		return nil, errs.ObjectNotFound
	}
	files, err := d.files(ctx)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if files[i].Name == name {
			return d.fileObj(ctx, &files[i], d.GetRootPath()), nil
		}
	}
	return nil, errs.ObjectNotFound
}

func (d *EnteShare) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	defer d.flushPendingDeviceToken()
	obj, ok := file.(*ente.EnteObj)
	if !ok || obj.Dec == nil {
		return nil, fmt.Errorf("[EnteShare] unsupported object type %T", file)
	}
	f := obj.Dec
	if args.Type == "thumb" {
		if !f.HasThumb {
			return nil, fmt.Errorf("[EnteShare] no thumbnail: %s", f.Name)
		}
		return &model.Link{
			RangeReader: ente.NewRangeReader(d.client, func(ctx context.Context) (string, error) {
				return d.client.GetThumbURL(ctx, f.ID)
			}, f.FileKey, f.ThumbnailDecryptionHeader),
			// info.thumbSize is the plaintext thumbnail size, same convention as
			// info.fileSize for downloads.
			ContentLength: f.ThumbSize,
		}, nil
	}
	return &model.Link{
		RangeReader: ente.NewRangeReader(d.client, func(ctx context.Context) (string, error) {
			return d.client.GetDownloadURL(ctx, f.ID)
		}, f.FileKey, f.FileDecryptionHeader),
		ContentLength: f.Size,
	}, nil
}

func (d *EnteShare) rootObj() model.Obj {
	return &model.Object{
		Path:     d.GetRootPath(),
		Name:     "root",
		Modified: d.GetStorage().Modified,
		Mask:     model.Locked,
		IsFolder: true,
	}
}

func (d *EnteShare) listFiles(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	files, err := d.files(ctx)
	if err != nil {
		return nil, err
	}
	objs := make([]model.Obj, 0, len(files))
	for i := range files {
		objs = append(objs, d.fileObj(ctx, &files[i], dir.GetPath()))
	}
	// The worker lists files name-ascending; LocalSort only reorders when the
	// mount sets order_by, so the driver provides the default order itself.
	model.SortFiles(objs, "name", "asc")
	return objs, nil
}

func (d *EnteShare) files(ctx context.Context) ([]ente.EnteDecryptedFile, error) {
	diff, err := ente.PaginateEnteDiff(ctx, func(ctx context.Context, sinceTime int64) (*ente.EnteDiffResponse, error) {
		return d.client.GetPublicDiff(ctx, sinceTime)
	})
	if err != nil {
		return nil, err
	}
	decrypted := make([]ente.EnteDecryptedFile, 0, len(diff))
	for _, f := range diff {
		df, err := ente.DecryptEnteFile(f, d.collectionKey)
		if err != nil {
			log.Warnf("[EnteShare] skip file %d: %v", f.ID, err)
			continue
		}
		decrypted = append(decrypted, *df)
	}
	// Keep paginateEnteDiff's (updationTime, id) order: dedupe suffixes are
	// assigned in this order, matching the worker.
	ente.DedupeNames(decrypted, func(f *ente.EnteDecryptedFile) *string { return &f.Name })
	return decrypted, nil
}

func (d *EnteShare) fileObj(ctx context.Context, f *ente.EnteDecryptedFile, dirPath string) model.Obj {
	obj := &ente.EnteObj{ObjThumb: model.ObjThumb{Object: model.Object{
		ID:       strconv.FormatInt(f.ID, 10),
		Path:     stdpath.Join("/", f.Name),
		Name:     f.Name,
		Size:     f.Size,
		Modified: time.UnixMilli(f.ModifiedMs),
	}}, Dec: f}
	if f.HasThumb {
		obj.Thumbnail = model.Thumbnail{Thumbnail: ente.ThumbURL(ctx, d.GetStorage().MountPath, dirPath, f.Name)}
	}
	return obj
}

// consumePendingDeviceToken returns and clears the token captured since the
// last flush, mirroring the worker's consumePendingDeviceToken.
func (d *EnteShare) consumePendingDeviceToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.pendingDeviceToken
	d.pendingDeviceToken = ""
	return t
}

// flushPendingDeviceToken persists a freshly issued linkDeviceToken so that a
// restart does not occupy a second device slot.
func (d *EnteShare) flushPendingDeviceToken() {
	t := d.consumePendingDeviceToken()
	if t == "" || t == d.DeviceToken {
		return
	}
	d.DeviceToken = t
	if d.saveStorage != nil {
		d.saveStorage()
	}
}
