package ente

// Ente wire types: the museum server JSON shapes. Field names are the
// server's camelCase keys; the addition structs live in meta.go.

type EnteCollectionUser struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

type EnteCollection struct {
	ID                  int64              `json:"id"`
	Owner               EnteCollectionUser `json:"owner"`
	EncryptedKey        string             `json:"encryptedKey"`
	KeyDecryptionNonce  string             `json:"keyDecryptionNonce"`
	Name                string             `json:"name"`
	EncryptedName       string             `json:"encryptedName"`
	NameDecryptionNonce string             `json:"nameDecryptionNonce"`
	Type                string             `json:"type"`
	UpdationTime        int64              `json:"updationTime"`
	IsDeleted           bool               `json:"isDeleted"`
	MagicMetadata       *EnteMagicMetadata `json:"magicMetadata"`
	PubMagicMetadata    *EnteMagicMetadata `json:"pubMagicMetadata"`
	SharedMagicMetadata *EnteMagicMetadata `json:"sharedMagicMetadata"`
}

type EnteFileAttributes struct {
	EncryptedData    string `json:"encryptedData"`
	DecryptionHeader string `json:"decryptionHeader"`
}

type EnteFileInfo struct {
	FileSize  int64 `json:"fileSize"`
	ThumbSize int64 `json:"thumbSize"`
}

type EnteMagicMetadata struct {
	Data   string `json:"data"`
	Header string `json:"header"`
}

type EnteDiffFile struct {
	ID                 int64               `json:"id"`
	OwnerID            int64               `json:"ownerID"`
	CollectionID       int64               `json:"collectionID"`
	EncryptedKey       string              `json:"encryptedKey"`
	KeyDecryptionNonce string              `json:"keyDecryptionNonce"`
	File               *EnteFileAttributes `json:"file"`
	Thumbnail          *EnteFileAttributes `json:"thumbnail"`
	Metadata           *EnteFileAttributes `json:"metadata"`
	IsDeleted          bool                `json:"isDeleted"`
	UpdationTime       int64               `json:"updationTime"`
	MagicMetadata      *EnteMagicMetadata  `json:"magicMetadata"`
	PubMagicMetadata   *EnteMagicMetadata  `json:"pubMagicMetadata"`
	Info               *EnteFileInfo       `json:"info"`
}

type EnteDiffResponse struct {
	Diff    []EnteDiffFile `json:"diff"`
	HasMore bool           `json:"hasMore"`
}
