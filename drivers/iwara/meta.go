package iwara

import (
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	Endpoint     string `json:"endpoint" type:"text" default:"https://www.iwara.zip"`
	APIKey1      string `json:"api_key_1" required:"true" help:"API key 1 generated on the iwara.zip account settings page"`
	APIKey2      string `json:"api_key_2" required:"true" help:"API key 2 generated on the iwara.zip account settings page"`
	RootFolderID string `json:"root_folder_id" help:"Leave empty to mount the account root folder"`
}

type IwaraZip struct {
	model.Storage
	Addition

	tokenMu   sync.Mutex
	accessToken string
	accountID   string
	tokenUsed   time.Time
}

var config = driver.Config{
	Name:        "IwaraZip",
	DefaultRoot: "/",
}

func init() {
	op.RegisterDriver(func() driver.Driver { return &IwaraZip{} })
}
