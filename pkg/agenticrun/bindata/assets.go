package bindata

import (
	"embed"
)

//go:embed assets/*
var f embed.FS

func Asset(name string) ([]byte, error) {
	return f.ReadFile(name)
}

func MustAsset(name string) []byte {
	data, err := f.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}
