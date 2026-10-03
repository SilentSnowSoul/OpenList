package iwara

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/go-resty/resty/v2"
)

func iwaraTestClient(t *testing.T) func() {
	t.Helper()
	old := base.RestyClient
	base.RestyClient = resty.New()
	return func() { base.RestyClient = old }
}

func TestListReacquiresTokenAndMapsObjects(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var authorizations, listings int
	var listedRoot string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/authorize":
			authorizations++
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
		case "/api/v2/folder/listing":
			listings++
			_ = r.ParseForm()
			listedRoot = r.FormValue("parent_folder_id")
			if listings == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"response":"access token expired","_status":"error"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"folders":[{"id":"folder","folderName":"Docs","date_updated":"2025-01-02 03:04:05"}],"files":[{"id":"file","filename":"a.txt","size":42,"date_added":"2025-01-03 03:04:05"}]},"_status":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two", RootFolderID: "root-123"}}
	objs, err := d.List(context.Background(), &model.Object{}, model.ListArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if authorizations != 2 || listings != 2 || listedRoot != "root-123" {
		t.Fatalf("expected one token reacquire and configured root ID, authorizations=%d listings=%d root=%q", authorizations, listings, listedRoot)
	}
	if len(objs) != 2 || !objs[0].IsDir() || objs[0].GetName() != "Docs" || objs[0].ModTime().IsZero() || objs[1].GetName() != "a.txt" || objs[1].GetSize() != 42 || objs[1].ModTime().IsZero() {
		t.Fatalf("unexpected mapped objects: %#v", objs)
	}
}

func TestStatusErrorIsPassedThrough(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		_, _ = w.Write([]byte(`{"response":"quota exceeded verbatim","_status":"error"}`))
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two"}}
	_, err := d.List(context.Background(), &model.Object{}, model.ListArgs{})
	if err == nil || err.Error() != "quota exceeded verbatim" {
		t.Fatalf("expected original API error, got %v", err)
	}
}

func TestLinkReturnsSignedURL(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var requestedID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		_ = r.ParseForm()
		requestedID = r.FormValue("file_id")
		_, _ = w.Write([]byte(`{"data":{"download_url":"https://download.example/signed?download_token=x"},"_status":"success"}`))
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two"}}
	link, err := d.Link(context.Background(), &model.Object{ID: "file-1"}, model.LinkArgs{})
	if err != nil || link.URL != "https://download.example/signed?download_token=x" || requestedID != "file-1" {
		t.Fatalf("unexpected link result: link=%#v id=%q err=%v", link, requestedID, err)
	}
}

func TestWriteOperationsSendExpectedEndpointsAndIDs(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var path string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path = r.URL.Path
		body = map[string]interface{}{}
		if strings.HasSuffix(path, "/authorize") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		_ = r.ParseForm()
		for key, values := range r.PostForm {
			if len(values) > 0 {
				body[key] = values[0]
			}
		}
		_, _ = w.Write([]byte(`{"data":{"id":"result","folderName":"result","filename":"result"},"_status":"success"}`))
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two", RootFolderID: "root-123"}}
	parent := &model.Object{ID: "parent", IsFolder: true}
	file := &model.Object{ID: "file"}
	if _, err := d.MakeDir(context.Background(), parent, "new"); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/folder/create" || body["parent_id"] != "parent" || body["folder_name"] != "new" {
		t.Fatalf("unexpected mkdir request: %s %#v", path, body)
	}
	if _, err := d.Move(context.Background(), file, parent); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/file/move" || body["file_id"] != "file" || body["new_parent_folder_id"] != "parent" {
		t.Fatalf("unexpected file move request: %s %#v", path, body)
	}
	if _, err := d.Move(context.Background(), parent, &model.Object{ID: "dest", IsFolder: true}); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/folder/move" || body["folder_id"] != "parent" || body["new_parent_folder_id"] != "dest" {
		t.Fatalf("unexpected folder move request: %s %#v", path, body)
	}
	if _, err := d.Rename(context.Background(), file, "renamed"); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/file/edit" || body["file_id"] != "file" || body["filename"] != "renamed" {
		t.Fatalf("unexpected file rename request: %s %#v", path, body)
	}
	if _, err := d.Rename(context.Background(), parent, "renamed-folder"); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/folder/edit" || body["folder_id"] != "parent" || body["folder_name"] != "renamed-folder" {
		t.Fatalf("unexpected folder rename request: %s %#v", path, body)
	}
	if _, err := d.Copy(context.Background(), file, parent); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/file/copy" || body["file_id"] != "file" || body["copy_to_folder_id"] != "parent" {
		t.Fatalf("unexpected file copy request: %s %#v", path, body)
	}
	if _, err := d.Copy(context.Background(), parent, parent); err == nil {
		t.Fatal("expected folder copy to be unsupported")
	}
	if err := d.Remove(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/file/delete" || body["file_id"] != "file" {
		t.Fatalf("unexpected file remove request: %s %#v", path, body)
	}
	if err := d.Remove(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/folder/delete" || body["folder_id"] != "parent" {
		t.Fatalf("unexpected folder remove request: %s %#v", path, body)
	}
}

func TestWriteOperationsUseConfiguredRootFolder(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var path string
	var body map[string]string
	var uploadContent string
	authorizations, uploads := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			authorizations++
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		path = r.URL.Path
		body = map[string]string{}
		if path == "/api/v2/file/upload" {
			uploads++
			if err := r.ParseMultipartForm(1024 * 1024); err != nil {
				t.Errorf("multipart parse: %v", err)
			}
			body["folder_id"] = r.FormValue("folder_id")
			file, _, err := r.FormFile("upload_file")
			if err == nil {
				defer file.Close()
				content, _ := io.ReadAll(file)
				uploadContent = string(content)
			}
		} else {
			_ = r.ParseForm()
			for key, values := range r.PostForm {
				if len(values) > 0 {
					body[key] = values[0]
				}
			}
		}
		if path == "/api/v2/file/upload" && uploads == 1 {
			_, _ = w.Write([]byte(`{"_status":"error","response":"Could not validate access_token and account_id"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"result","filename":"result"},"_status":"success"}`))
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two", RootFolderID: "root-123"}}
	empty := &model.Object{}
	file := &model.Object{ID: "file"}
	folder := &model.Object{ID: "folder", IsFolder: true}
	check := func(wantPath, key string) {
		t.Helper()
		if path != wantPath || body[key] != "root-123" {
			t.Fatalf("expected %s %s=root-123, got %s %#v", wantPath, key, path, body)
		}
	}
	if _, err := d.MakeDir(context.Background(), empty, "new"); err != nil {
		t.Fatal(err)
	}
	check("/api/v2/folder/create", "parent_id")
	if _, err := d.Move(context.Background(), file, empty); err != nil {
		t.Fatal(err)
	}
	check("/api/v2/file/move", "new_parent_folder_id")
	if _, err := d.Move(context.Background(), folder, empty); err != nil {
		t.Fatal(err)
	}
	check("/api/v2/folder/move", "new_parent_folder_id")
	if _, err := d.Copy(context.Background(), file, empty); err != nil {
		t.Fatal(err)
	}
	check("/api/v2/file/copy", "copy_to_folder_id")
	if _, err := d.Put(context.Background(), empty, &stream.FileStream{Obj: &model.Object{Name: "x.txt", Size: 3}, Reader: io.NopCloser(strings.NewReader("abc"))}, func(float64) {}); err == nil {
		t.Fatal("expected token-invalid upload to fail when the consumed stream cannot be replayed")
	}
	if uploads != 1 || authorizations != 2 || body["folder_id"] != "root-123" || uploadContent != "abc" {
		t.Fatalf("unsafe upload retry or incorrect root destination: uploads=%d authorizations=%d form=%#v content=%q", uploads, authorizations, body, uploadContent)
	}
}


func TestLinkAndUploadErrorsPassThrough(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var uploadError string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		if r.URL.Path == "/api/v2/file/upload" {
			_, _ = w.Write([]byte(`{"response":"upload quota exceeded","_status":"error"}`))
			return
		}
		if r.URL.Path == "/api/v2/file/download" {
			_, _ = w.Write([]byte(`{"response":"download forbidden","_status":"error"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two"}}
	if _, err := d.Link(context.Background(), &model.Object{ID: "file"}, model.LinkArgs{}); err == nil || err.Error() != "download forbidden" {
		t.Fatalf("expected original download error, got %v", err)
	}
	stream := &stream.FileStream{Obj: &model.Object{Name: "x.txt", Size: 3}, Reader: strings.NewReader("abc")}
	_, err := d.Put(context.Background(), &model.Object{ID: "folder"}, stream, func(float64) {})
	if err == nil {
		t.Fatal("expected upload error")
	}
	uploadError = err.Error()
	if uploadError != "upload quota exceeded" {
		t.Fatalf("expected original upload error, got %q", uploadError)
	}
}
func TestPutUsesMultipartWholeFile(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var gotName, gotFolder, gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		if r.URL.Path == "/api/v2/folder/listing" {
			_, _ = w.Write([]byte(`{"data":{"folders":[],"files":[{"id":"uploaded","filename":"x.txt","size":3,"date_added":"2025-01-03T03:04:05Z"}]},"_status":"success"}`))
			return
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Errorf("multipart parse: %v", err)
		}
		gotFolder = r.FormValue("folder_id")
		if file, header, err := r.FormFile("upload_file"); err == nil {
			defer file.Close()
			gotName = header.Filename
			buf := make([]byte, 3)
			_, _ = file.Read(buf)
			gotContent = string(buf)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"uploaded","filename":"x.txt","size":3},"_status":"success"}`))
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two", RootFolderID: "root-123"}}
	stream := &stream.FileStream{Obj: &model.Object{Name: "x.txt", Size: 3}, Reader: strings.NewReader("abc")}
	uploaded, err := d.Put(context.Background(), &model.Object{ID: "folder"}, stream, func(float64) {})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := d.List(context.Background(), &model.Object{ID: "folder"}, model.ListArgs{})
	if err != nil || uploaded.GetID() != "uploaded" || len(listed) != 1 || listed[0].GetName() != "x.txt" {
		t.Fatalf("uploaded file was not visible in listing: uploaded=%#v listed=%#v err=%v", uploaded, listed, err)
	}
	if gotName != "x.txt" || gotFolder != "folder" || gotContent != "abc" {
		t.Fatalf("unexpected upload: name=%q folder=%q content=%q", gotName, gotFolder, gotContent)
	}
}

func TestUnauthorizedEnvelopeReacquiresToken(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var authorizations, listings, uploads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/authorize":
			authorizations++
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
		case "/api/v2/folder/listing":
			listings++
			if listings == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"response":"generic request failure","_status":"error"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"folders":[],"files":[]},"_status":"success"}`))
		case "/api/v2/file/upload":
			uploads++
			if uploads == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"response":"generic request failure","_status":"error"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"uploaded","filename":"x.txt","size":3},"_status":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two"}}
	if _, err := d.List(context.Background(), &model.Object{}, model.ListArgs{}); err != nil {
		t.Fatalf("listing failed after unauthorized retry: %v", err)
	}
	file := &stream.FileStream{Obj: &model.Object{Name: "x.txt", Size: 3}, Reader: strings.NewReader("abc")}
	if _, err := d.Put(context.Background(), &model.Object{ID: "folder"}, file, func(float64) {}); err != nil {
		t.Fatalf("upload failed after unauthorized retry: %v", err)
	}
	if authorizations != 3 || listings != 2 || uploads != 2 {
		t.Fatalf("expected listing and upload retries with one token reacquire each, authorizations=%d listings=%d uploads=%d", authorizations, listings, uploads)
	}
}

func TestTooManyRequestsIsNotRetried(t *testing.T) {
	cleanup := iwaraTestClient(t)
	defer cleanup()
	var listingRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/authorize") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"token","account_id":"acct"},"_status":"success"}`))
			return
		}
		listingRequests++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"response":"rate limited","_status":"error"}`))
	}))
	defer srv.Close()
	d := &IwaraZip{Addition: Addition{Endpoint: srv.URL, APIKey1: "one", APIKey2: "two"}}
	_, err := d.List(context.Background(), &model.Object{}, model.ListArgs{})
	if err == nil || err.Error() != "rate limited" || listingRequests != 1 {
		t.Fatalf("expected one un-retried 429 and original message, requests=%d err=%v", listingRequests, err)
	}
}
