package ente_share

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

// Addition is the public-share Ente storage configuration.
type Addition struct {
	driver.RootPath
	ShareURL    string `json:"share_url" required:"true" help:"Ente public share URL (https://share.ente.io/c/<token>#<key>)"`
	Endpoint    string `json:"endpoint" default:"https://api.ente.com" help:"Ente API endpoint, change it when self-hosting museum"`
	DeviceToken string `json:"device_token" ignore:"true" help:"linkDeviceToken issued by the server, persisted to reuse the device slot"`
}

var config = driver.Config{
	Name:      "EnteShare",
	LocalSort: true,
	OnlyProxy: true,
	NoLinkURL: true,
	NoUpload:  true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &EnteShare{}
	})
}
