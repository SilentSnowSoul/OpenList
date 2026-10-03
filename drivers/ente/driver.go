package ente

import (
	"context"
	"errors"
	"fmt"
	stdpath "path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	log "github.com/sirupsen/logrus"
)

var _ driver.Driver = (*Ente)(nil)

// Ente is the account-level Ente storage driver: every collection of the
// account is exposed as a folder under the root directory.
type Ente struct {
	model.Storage
	Addition

	client    *Client
	masterKey []byte
	secretKey []byte
}

// EnteObj carries the decrypted Ente payload alongside the OpenList object so
// that Link can stream-decrypt without another network round trip.
type EnteObj struct {
	model.ObjThumb
	Dec *EnteDecryptedFile
}

func (d *Ente) Config() driver.Config {
	return config
}

func (d *Ente) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Ente) Init(ctx context.Context) error {
	switch {
	case d.Password != "" && (d.Email != "" || d.Token != ""):
		return d.initPasswordMode(ctx)
	case d.Token != "" && d.MasterKey != "":
		return d.initCredentialMode()
	default:
		return errors.New("no way to authenticate: fill email/password, token/password or token/master_key")
	}
}

// initCredentialMode uses pre-provisioned APIPages credentials as-is.
func (d *Ente) initCredentialMode() error {
	masterKey, err := fromB64(d.MasterKey)
	if err != nil {
		return fmt.Errorf("[Ente] invalid master_key: %w", err)
	}
	d.masterKey = masterKey
	if d.SecretKey != "" {
		secretKey, err := fromB64(d.SecretKey)
		if err != nil {
			return fmt.Errorf("[Ente] invalid secret_key: %w", err)
		}
		d.secretKey = secretKey
	}
	d.client = NewClient(d.Endpoint, d.Token, authTokenHeader)
	return nil
}

// initPasswordMode authenticates with the account password. A persisted
// token is tried first (local keyAttributes decrypt, no SRP handshake); on
// failure the full SRP login runs and the new token is written back. The
// master and secret keys stay in memory only.
func (d *Ente) initPasswordMode(ctx context.Context) error {
	if d.Token != "" {
		if masterKey, secretKey, ok := d.tryTokenFastPath(ctx); ok {
			d.masterKey = masterKey
			d.secretKey = secretKey
			if d.MasterKey != "" || d.SecretKey != "" {
				d.MasterKey = ""
				d.SecretKey = ""
				op.MustSaveDriverStorage(d)
			}
			return nil
		}
	}
	if d.Email == "" {
		return errors.New("token invalid or expired: refresh the token or set email/password for SRP login")
	}
	cred, err := d.loginPassword(ctx)
	if err != nil {
		return err
	}
	d.Token = cred.token
	d.MasterKey = ""
	d.SecretKey = ""
	op.MustSaveDriverStorage(d)
	d.client = NewClient(d.Endpoint, d.Token, authTokenHeader)
	d.masterKey = cred.masterKey
	d.secretKey = cred.secretKey
	return nil
}

// tryTokenFastPath decrypts keyAttributes locally with the persisted token;
// any failure (revoked token, changed password) falls back to SRP login.
func (d *Ente) tryTokenFastPath(ctx context.Context) (masterKey, secretKey []byte, ok bool) {
	client := NewClient(d.Endpoint, d.Token, authTokenHeader)
	attrs, err := client.getKeyAttributes(ctx)
	if err != nil {
		return nil, nil, false
	}
	kekSalt, err := fromB64(attrs.KEKSalt)
	if err != nil {
		return nil, nil, false
	}
	kek, err := deriveKEK(d.Password, kekSalt, attrs.OpsLimit, attrs.MemLimit)
	if err != nil {
		return nil, nil, false
	}
	masterKey, secretKey, err = openKeyAttributes(*attrs, kek)
	if err != nil {
		return nil, nil, false
	}
	d.client = client
	return masterKey, secretKey, true
}

func (d *Ente) Drop(_ context.Context) error {
	return nil
}

func (d *Ente) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	if d.isRoot(dir) {
		return d.listCollections(ctx)
	}
	return d.listFiles(ctx, dir)
}

func (d *Ente) Get(ctx context.Context, path string) (model.Obj, error) {
	path = utils.FixAndCleanPath(path)
	// op.Get resolves "/" through IRootPath, so this is a defensive fallback.
	if path == "/" {
		return RootObj(d.GetRootPath(), d.GetStorage().Modified), nil
	}
	dirPath, name := stdpath.Split(path)
	if dirPath = strings.Trim(dirPath, "/"); dirPath == "" {
		objs, err := d.listCollections(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range objs {
			if obj.GetName() == name {
				return obj, nil
			}
		}
		return nil, errs.ObjectNotFound
	}
	objs, err := d.listFiles(ctx, &model.Object{Path: stdpath.Join("/", dirPath)})
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		if obj.GetName() == name {
			return obj, nil
		}
	}
	return nil, errs.ObjectNotFound
}

func (d *Ente) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	obj, ok := file.(*EnteObj)
	if !ok || obj.Dec == nil {
		return nil, fmt.Errorf("[Ente] unsupported object type %T", file)
	}
	f := obj.Dec
	if args.Type == "thumb" {
		if !f.HasThumb {
			return nil, fmt.Errorf("[Ente] no thumbnail: %s", f.Name)
		}
		return &model.Link{
			RangeReader: NewRangeReader(d.client, func(ctx context.Context) (string, error) {
				return d.client.GetFileThumbURL(ctx, f.ID)
			}, f.FileKey, f.ThumbnailDecryptionHeader),
			// info.thumbSize is the plaintext thumbnail size, same convention as
			// info.fileSize for downloads.
			ContentLength: f.ThumbSize,
		}, nil
	}
	return &model.Link{
		RangeReader: NewRangeReader(d.client, func(ctx context.Context) (string, error) {
			return d.client.GetFileDownloadURL(ctx, f.ID)
		}, f.FileKey, f.FileDecryptionHeader),
		ContentLength: f.Size,
	}, nil
}

func (d *Ente) isRoot(dir model.Obj) bool {
	return dir.GetPath() == d.GetRootPath()
}

func (d *Ente) listCollections(ctx context.Context) ([]model.Obj, error) {
	collections, err := d.client.GetCollections(ctx)
	if err != nil {
		return nil, err
	}
	decrypted := make([]EnteDecryptedCollection, 0, len(collections))
	for _, c := range collections {
		if c.IsDeleted {
			continue
		}
		dc, err := decryptEnteCollection(c, d.masterKey, d.secretKey)
		if err != nil {
			log.Warnf("[Ente] skip collection %d: %v", c.ID, err)
			continue
		}
		decrypted = append(decrypted, *dc)
	}
	sort.Slice(decrypted, func(i, j int) bool {
		if decrypted[i].ModifiedMs != decrypted[j].ModifiedMs {
			return decrypted[i].ModifiedMs < decrypted[j].ModifiedMs
		}
		return decrypted[i].ID < decrypted[j].ID
	})
	DedupeNames(decrypted, func(c *EnteDecryptedCollection) *string { return &c.Name })
	if !d.ShowHidden {
		visible := make([]EnteDecryptedCollection, 0, len(decrypted))
		for _, c := range decrypted {
			if !c.Hidden {
				visible = append(visible, c)
			}
		}
		decrypted = visible
	}
	objs := make([]model.Obj, 0, len(decrypted))
	for i := range decrypted {
		c := &decrypted[i]
		objs = append(objs, &EnteObj{ObjThumb: model.ObjThumb{Object: model.Object{
			ID:       "collection-" + strconv.FormatInt(c.ID, 10),
			Path:     stdpath.Join("/", c.Name),
			Name:     c.Name,
			Modified: time.UnixMilli(c.ModifiedMs),
			Mask:     model.Locked,
			IsFolder: true,
		}}})
	}
	// The worker lists collections name-ascending; LocalSort only reorders when
	// the mount sets order_by, so the driver provides the default order itself.
	// Dedupe suffixes were already assigned in (modifiedMs, id) order above.
	model.SortFiles(objs, "name", "asc")
	return objs, nil
}

func (d *Ente) listFiles(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	collectionName := stdpath.Base(dir.GetPath())
	collection, err := d.findCollection(ctx, collectionName)
	if err != nil {
		return nil, err
	}
	files, err := PaginateEnteDiff(ctx, func(ctx context.Context, sinceTime int64) (*EnteDiffResponse, error) {
		return d.client.GetCollectionDiff(ctx, collection.ID, sinceTime)
	})
	if err != nil {
		return nil, err
	}
	decrypted := make([]EnteDecryptedFile, 0, len(files))
	for _, f := range files {
		df, err := DecryptEnteFile(f, collection.Key)
		if err != nil {
			log.Warnf("[Ente] skip file %d: %v", f.ID, err)
			continue
		}
		decrypted = append(decrypted, *df)
	}
	// Keep paginateEnteDiff's (updationTime, id) order: dedupe suffixes are
	// assigned in this order, matching the worker.
	DedupeNames(decrypted, func(f *EnteDecryptedFile) *string { return &f.Name })
	objs := make([]model.Obj, 0, len(decrypted))
	for i := range decrypted {
		f := &decrypted[i]
		obj := &EnteObj{ObjThumb: model.ObjThumb{Object: model.Object{
			ID:       strconv.FormatInt(f.ID, 10),
			Path:     stdpath.Join("/", collectionName, f.Name),
			Name:     f.Name,
			Size:     f.Size,
			Modified: time.UnixMilli(f.ModifiedMs),
		}}, Dec: f}
		if f.HasThumb {
			obj.Thumbnail = model.Thumbnail{Thumbnail: ThumbURL(ctx, d.GetStorage().MountPath, dir.GetPath(), f.Name)}
		}
		objs = append(objs, obj)
	}
	// The worker lists files name-ascending; LocalSort only reorders when the
	// mount sets order_by, so the driver provides the default order itself.
	model.SortFiles(objs, "name", "asc")
	return objs, nil
}

func (d *Ente) findCollection(ctx context.Context, name string) (*EnteDecryptedCollection, error) {
	collections, err := d.client.GetCollections(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range collections {
		if c.IsDeleted {
			continue
		}
		dc, err := decryptEnteCollection(c, d.masterKey, d.secretKey)
		if err != nil {
			log.Warnf("[Ente] skip collection %d: %v", c.ID, err)
			continue
		}
		if dc.Name == name {
			return dc, nil
		}
	}
	return nil, errs.ObjectNotFound
}
