// filex-passwd generates a bcrypt password hash suitable for use in the
// filex config.yaml password_hash field.
//
// Usage:
//
//	filex-passwd [password]
//
// If no password argument is given, the password is read from the terminal
// without echo (recommended).
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

func main() {
	var password string

	if len(os.Args) >= 2 {
		password = os.Args[1]
	} else {
		fmt.Fprint(os.Stderr, "Password: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr) // newline after the hidden input
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading password: %v\n", err)
			os.Exit(1)
		}
		password = string(raw)
	}

	if password == "" {
		fmt.Fprintln(os.Stderr, "password must not be empty")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(hash))
}
