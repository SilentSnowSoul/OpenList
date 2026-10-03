package ente

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

// Addition is the account-level Ente storage configuration.
type Addition struct {
	driver.RootPath
	Endpoint    string `json:"endpoint" default:"https://api.ente.com" help:"Ente API endpoint, change it when self-hosting museum"`
	Email       string `json:"email" help:"Ente account email; optional credential combos: email + password, token + password, token + master_key"`
	Password    string `json:"password" help:"Ente account password; set with email, or with token to decrypt keys locally"`
	TwoFASecret string `json:"two_fa_secret" help:"TOTP secret of accounts with two-factor enabled; the verification code is generated automatically"`
	Token       string `json:"token" help:"Ente app token (X-Auth-Token); password login fills it in automatically"`
	MasterKey   string `json:"master_key" help:"Ente account master key (base64); used with token when password is not set"`
	SecretKey   string `json:"secret_key" help:"Ente account secret key (base64); shared albums are skipped without it"`
	ShowHidden  bool   `json:"show_hidden" default:"false" help:"Show hidden albums (visibility=2)"`
}

var config = driver.Config{
	Name:        "Ente",
	LocalSort:   true,
	OnlyProxy:   true,
	NoLinkURL:   true,
	NoUpload:    true,
	DefaultRoot: "root",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Ente{}
	})
}
