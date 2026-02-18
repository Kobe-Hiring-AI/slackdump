package server

// Config holds the server configuration.
type Config struct {
	Addr          string `json:"addr"`
	DataDir       string `json:"data_dir"`
	AdminKey      string `json:"-"`
	EncryptionKey []byte `json:"-"` // 32 bytes for AES-256-GCM
	MaxConcurrent int    `json:"max_concurrent"`
}
