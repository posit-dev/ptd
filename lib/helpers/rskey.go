package helpers

import "github.com/rstudio/rskey/crypt"

func RsKeyGenerate() string {
	// "it never returns an error, despite its function signature"
	key, _ := crypt.NewKey()
	return key.HexString()
}
