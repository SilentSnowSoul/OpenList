package iwara

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"


	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	streamutil "github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/go-resty/resty/v2"
)

const tokenIdleTimeout = time.Hour

type apiRequestError struct {
	err         error
	tokenInvalid bool
}

func (e *apiRequestError) Error() string { return e.err.Error() }
func (e *apiRequestError) Unwrap() error { return e.err }

func (d *IwaraZip) apiURL(method string) string {
	return d.Endpoint + "/api/v2/" + strings.TrimLeft(method, "/")
}

func responseMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return message
	}
	return string(raw)
}

func isTokenInvalid(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "access_token") ||
		strings.Contains(message, "access token") ||
		strings.Contains(message, "token expired") ||
		strings.Contains(message, "expired token") ||
		strings.Contains(message, "token invalid") ||
		strings.Contains(message, "invalid token") ||
		strings.Contains(message, "unauthoriz") ||
		strings.Contains(message, "authentication")
}

func (d *IwaraZip) authorize(ctx context.Context) error {
	var envelope apiEnvelope
	resp, err := base.RestyClient.R().SetContext(ctx).
		SetFormData(map[string]string{"key1": d.APIKey1, "key2": d.APIKey2}).
		Post(d.apiURL("authorize"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resp.Body(), &envelope); err != nil {
		if !resp.IsSuccess() {
			return errors.New(resp.String())
		}
		return err
	}
	if envelope.Status == "error" || envelope.LegacyStatus == "error" {
		return errors.New(responseMessage(envelope.Response))
	}
	if !resp.IsSuccess() {
		return errors.New(resp.String())
	}
	var data authData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return err
	}
	if data.AccessToken == "" || data.AccountID == "" {
		return errors.New("iwara authorization response missing access_token or account_id")
	}
	d.tokenMu.Lock()
	d.accessToken, d.accountID, d.tokenUsed = data.AccessToken, string(data.AccountID), time.Now()
	d.tokenMu.Unlock()
	return nil
}

func (d *IwaraZip) token(ctx context.Context) (string, string, error) {
	d.tokenMu.Lock()
	if d.accessToken != "" && time.Since(d.tokenUsed) < tokenIdleTimeout {
		d.tokenUsed = time.Now()
		token, account := d.accessToken, d.accountID
		d.tokenMu.Unlock()
		return token, account, nil
	}
	d.tokenMu.Unlock()
	if err := d.authorize(ctx); err != nil {
		return "", "", err
	}
	d.tokenMu.Lock()
	token, account := d.accessToken, d.accountID
	d.tokenMu.Unlock()
	return token, account, nil
}

func (d *IwaraZip) invalidate(token string) {
	d.tokenMu.Lock()
	if d.accessToken == token {
		d.accessToken, d.accountID = "", ""
	}
	d.tokenMu.Unlock()
}

func (d *IwaraZip) post(ctx context.Context, method, token, account string, body interface{}) (*resty.Response, error) {
	params := map[string]string{"access_token": token, "account_id": account}
	if values, ok := body.(map[string]interface{}); ok {
		for key, value := range values {
			params[key] = fmt.Sprint(value)
		}
	} else if values, ok := body.(map[string]string); ok {
		for key, value := range values {
			params[key] = value
		}
	}
	return base.RestyClient.R().SetContext(ctx).SetFormData(params).Post(d.apiURL(method))
}

func envelopeError(resp *resty.Response) *apiRequestError {
	var envelope apiEnvelope
	if err := json.Unmarshal(resp.Body(), &envelope); err != nil {
		if !resp.IsSuccess() {
			return &apiRequestError{err: errors.New(resp.String())}
		}
		return &apiRequestError{err: err}
	}
	if envelope.Status == "error" || envelope.LegacyStatus == "error" {
		message := responseMessage(envelope.Response)
		if message == "" {
			message = resp.String()
		}
		return &apiRequestError{err: errors.New(message), tokenInvalid: resp.StatusCode() == http.StatusUnauthorized || isTokenInvalid(message)}
	}
	if !resp.IsSuccess() {
		return &apiRequestError{err: errors.New(resp.String()), tokenInvalid: resp.StatusCode() == http.StatusUnauthorized}
	}
	return nil
}

func (d *IwaraZip) call(ctx context.Context, method, token, account string, body interface{}, result interface{}) *apiRequestError {
	resp, err := d.post(ctx, method, token, account, body)
	if err != nil {
		return &apiRequestError{err: err}
	}
	if requestErr := envelopeError(resp); requestErr != nil {
		return requestErr
	}
	if result != nil {
		var envelope apiEnvelope
		if err := json.Unmarshal(resp.Body(), &envelope); err != nil {
			return &apiRequestError{err: err}
		}
		if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
			if err := json.Unmarshal(envelope.Data, result); err != nil {
				return &apiRequestError{err: err}
			}
		}
	}
	return nil
}

// callFull decodes the whole response body, for endpoints whose payload
// lives outside the top-level data field (e.g. file/copy).
func (d *IwaraZip) callFull(ctx context.Context, method, token, account string, body interface{}, result interface{}) *apiRequestError {
	resp, err := d.post(ctx, method, token, account, body)
	if err != nil {
		return &apiRequestError{err: err}
	}
	if requestErr := envelopeError(resp); requestErr != nil {
		return requestErr
	}
	if err := json.Unmarshal(resp.Body(), result); err != nil {
		return &apiRequestError{err: err}
	}
	return nil
}

func (d *IwaraZip) requestCall(ctx context.Context, method string, body, result interface{}, call func(ctx context.Context, method, token, account string, body, result interface{}) *apiRequestError) error {
	token, account, err := d.token(ctx)
	if err != nil {
		return err
	}
	requestErr := call(ctx, method, token, account, body, result)
	if requestErr == nil {
		return nil
	}
	if !requestErr.tokenInvalid {
		return requestErr
	}
	d.invalidate(token)
	newToken, newAccount, err := d.token(ctx)
	if err != nil {
		return err
	}
	if retryErr := call(ctx, method, newToken, newAccount, body, result); retryErr != nil {
		return retryErr
	}
	return nil
}

func (d *IwaraZip) request(ctx context.Context, method string, body interface{}, result interface{}) error {
	return d.requestCall(ctx, method, body, result, d.call)
}

func folderID(dir model.Obj) string {
	if dir == nil {
		return ""
	}
	return dir.GetID()
}

func destinationFolderID(d *IwaraZip, dir model.Obj) string {
	if id := folderID(dir); id != "" {
		return id
	}
	return d.RootFolderID
}

func (d *IwaraZip) list(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	id := folderID(dir)
	if id == "" {
		id = d.RootFolderID
	}
	body := map[string]interface{}{}
	if id != "" {
		body["parent_folder_id"] = id
	}
	var listing listingData
	if err := d.request(ctx, "folder/listing", body, &listing); err != nil {
		return nil, err
	}
	objects := make([]model.Obj, 0, len(listing.Folders)+len(listing.Files))
	for _, folder := range listing.Folders {
		objects = append(objects, folderObject(folder))
	}
	for _, file := range listing.Files {
		objects = append(objects, fileObject(file))
	}
	return objects, nil
}


func objectTime(values ...string) time.Time {
	for _, value := range values {
		if value == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04:05 MST"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

func folderObject(folder apiFolder) model.Obj {
	return &model.Object{ID: string(folder.ID), Name: folder.Name, Size: folder.Size.int64(),
		Modified: objectTime(folder.DateUpdate, folder.DateAdded), IsFolder: true}
}

func fileObject(file apiFile) model.Obj {
	return &model.Object{ID: string(file.ID), Name: file.Name, Size: file.Size.int64(),
		Modified: objectTime(file.DateUpdate, file.DateAdded)}
}

func (d *IwaraZip) link(ctx context.Context, file model.Obj) (*model.Link, error) {
	var data downloadData
	if err := d.request(ctx, "file/download", map[string]interface{}{"file_id": file.GetID()}, &data); err != nil {
		return nil, err
	}
	if data.DownloadURL == "" {
		return nil, errors.New("iwara download response missing signed URL")
	}
	return &model.Link{URL: data.DownloadURL}, nil
}

func (d *IwaraZip) makeDir(ctx context.Context, parentDir model.Obj, name string) (model.Obj, error) {
	data := apiFolder{}
	body := map[string]interface{}{"folder_name": name}
	if id := destinationFolderID(d, parentDir); id != "" {
		body["parent_id"] = id
	}
	if err := d.request(ctx, "folder/create", body, &data); err != nil {
		return nil, err
	}
	return folderObject(data), nil
}

func (d *IwaraZip) move(ctx context.Context, src, dst model.Obj) (model.Obj, error) {
	body := map[string]interface{}{}
	if src.IsDir() {
		body["folder_id"] = src.GetID()
		body["new_parent_folder_id"] = destinationFolderID(d, dst)
		var data apiFolder
		if err := d.request(ctx, "folder/move", body, &data); err != nil {
			return nil, err
		}
		return folderObject(data), nil
	}
	body["file_id"] = src.GetID()
	body["new_parent_folder_id"] = destinationFolderID(d, dst)
	var data apiFile
	if err := d.request(ctx, "file/move", body, &data); err != nil {
		return nil, err
	}
	return fileObject(data), nil
}

func (d *IwaraZip) rename(ctx context.Context, src model.Obj, name string) (model.Obj, error) {
	if src.IsDir() {
		var data apiFolder
		if err := d.request(ctx, "folder/edit", map[string]interface{}{"folder_id": src.GetID(), "folder_name": name}, &data); err != nil {
			return nil, err
		}
		return folderObject(data), nil
	}
	var data apiFile
	if err := d.request(ctx, "file/edit", map[string]interface{}{"file_id": src.GetID(), "filename": name}, &data); err != nil {
		return nil, err
	}
	return fileObject(data), nil
}

func (d *IwaraZip) copy(ctx context.Context, src, dst model.Obj) (model.Obj, error) {
	if src.IsDir() {
		return nil, errs.NotImplement
	}
	var data copyResponse
	body := map[string]interface{}{"file_id": src.GetID(), "copy_to_folder_id": destinationFolderID(d, dst)}
	if err := d.requestCall(ctx, "file/copy", body, &data, d.callFull); err != nil {
		return nil, err
	}
	var file apiFile
	if len(data.NewFile.Data) != 0 {
		if err := json.Unmarshal(data.NewFile.Data, &file); err != nil {
			return nil, err
		}
	}
	return fileObject(file), nil
}

func (d *IwaraZip) remove(ctx context.Context, obj model.Obj) error {
	method, key := "file/delete", "file_id"
	if obj.IsDir() {
		method, key = "folder/delete", "folder_id"
	}
	return d.request(ctx, method, map[string]interface{}{key: obj.GetID()}, nil)
}

func (d *IwaraZip) put(ctx context.Context, dst model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	token, account, err := d.token(ctx)
	if err != nil {
		return nil, err
	}
	obj, requestErr := d.putOnce(ctx, dst, stream, stream, up, token, account)
	if requestErr == nil {
		return obj, nil
	}
	if !requestErr.tokenInvalid {
		return nil, requestErr
	}
	d.invalidate(token)
	token, account, err = d.token(ctx)
	if err != nil {
		return nil, err
	}
	file := stream.GetFile()
	if file == nil {
		return nil, requestErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	obj, requestErr = d.putOnce(ctx, dst, stream, file, up, token, account)
	if requestErr == nil {
		return obj, nil
	}
	return nil, requestErr
}

func (d *IwaraZip) putOnce(ctx context.Context, dst model.Obj, stream model.FileStreamer, source io.Reader, up driver.UpdateProgress, token, account string) (model.Obj, *apiRequestError) {
	reader := driver.NewLimitedUploadStream(ctx, &driver.ReaderUpdatingProgress{
		Reader:         &streamutil.SimpleReaderWithSize{Reader: source, Size: stream.GetSize()},
		UpdateProgress: up,
	})
	form := map[string]string{"access_token": token, "account_id": account}
	if id := destinationFolderID(d, dst); id != "" {
		form["folder_id"] = id
	}
	resp, err := base.RestyClient.R().SetContext(ctx).
		SetFileReader("upload_file", stream.GetName(), reader).
		SetFormData(form).
		Post(d.apiURL("file/upload"))
	if err != nil {
		return nil, &apiRequestError{err: err}
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(resp.Body(), &envelope); err != nil {
		if !resp.IsSuccess() {
			return nil, &apiRequestError{err: errors.New(resp.String())}
		}
		return nil, &apiRequestError{err: err}
	}
	if envelope.Status == "error" || envelope.LegacyStatus == "error" {
		message := responseMessage(envelope.Response)
		return nil, &apiRequestError{err: errors.New(message), tokenInvalid: resp.StatusCode() == http.StatusUnauthorized || isTokenInvalid(message)}
	}
	if !resp.IsSuccess() {
		return nil, &apiRequestError{err: errors.New(resp.String()), tokenInvalid: resp.StatusCode() == http.StatusUnauthorized}
	}
	var items []uploadItem
	if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, &items); err != nil {
			return nil, &apiRequestError{err: err}
		}
	}
	if len(items) == 0 || items[0].FileID == "" {
		return nil, &apiRequestError{err: errors.New("iwara upload response missing file_id")}
	}
	return &model.Object{ID: string(items[0].FileID), Name: items[0].Name, Size: items[0].Size.int64()}, nil
}


