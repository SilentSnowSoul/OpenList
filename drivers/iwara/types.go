package iwara

import "encoding/json"

type apiEnvelope struct {
	Data         json.RawMessage `json:"data"`
	Status       string          `json:"_status"`
	LegacyStatus string          `json:"status"`
	Response     json.RawMessage `json:"response"`
}
type authData struct {
	AccessToken string `json:"access_token"`
	AccountID   string `json:"account_id"`
}

type listingData struct {
	Folders []apiFolder `json:"folders"`
	Files   []apiFile   `json:"files"`
}

type apiFolder struct {
	ID         string `json:"id"`
	Name       string `json:"folderName"`
	Size       int64  `json:"size"`
	DateAdded  string `json:"date_added"`
	DateUpdate string `json:"date_updated"`
}

type apiFile struct {
	ID        string `json:"id"`
	Name      string `json:"filename"`
	Size      int64  `json:"size"`
	DateAdded string `json:"date_added"`
	DateUpdate string `json:"date_updated"`
}

type downloadData struct {
	DownloadURL string `json:"download_url"`
}
