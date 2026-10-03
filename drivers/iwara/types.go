package iwara

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// flexString accepts both JSON strings and numbers; the real API is
// inconsistent with its documented types.
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = flexString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = flexString(n.String())
	return nil
}

func (s flexString) int64() int64 {
	value, _ := strconv.ParseInt(string(s), 10, 64)
	return value
}

type apiEnvelope struct {
	Data         json.RawMessage `json:"data"`
	Status       string          `json:"_status"`
	LegacyStatus string          `json:"status"`
	Response     json.RawMessage `json:"response"`
}

type authData struct {
	AccessToken string     `json:"access_token"`
	AccountID   flexString `json:"account_id"`
}

type listingData struct {
	Folders []apiFolder `json:"folders"`
	Files   []apiFile   `json:"files"`
}

type apiFolder struct {
	ID         flexString `json:"id"`
	Name       string     `json:"folderName"`
	Size       flexString `json:"totalSize"`
	DateAdded  string     `json:"date_added"`
	DateUpdate string     `json:"date_updated"`
}

type apiFile struct {
	ID         flexString `json:"id"`
	Name       string     `json:"filename"`
	Size       flexString `json:"fileSize"`
	DateAdded  string     `json:"date_added"`
	DateUpdate string     `json:"date_updated"`
}

type uploadItem struct {
	Name   string     `json:"name"`
	Size   flexString `json:"size"`
	FileID flexString `json:"file_id"`
}

type copyResponse struct {
	OriginalFile apiEnvelope `json:"original_file"`
	NewFile      apiEnvelope `json:"new_file"`
}

type downloadData struct {
	DownloadURL string `json:"download_url"`
}
