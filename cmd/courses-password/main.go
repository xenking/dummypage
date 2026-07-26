package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	password, err := io.ReadAll(io.LimitReader(os.Stdin, 73))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read password:", err)
		os.Exit(1)
	}
	password = bytes.TrimSuffix(password, []byte("\n"))
	if len(password) < 12 {
		fmt.Fprintln(os.Stderr, "password must be at least 12 bytes")
		os.Exit(1)
	}
	if len(password) > 72 {
		fmt.Fprintln(os.Stderr, "password must be at most 72 bytes")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword(password, 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}
	fmt.Println(base64.RawStdEncoding.EncodeToString(hash))
}
