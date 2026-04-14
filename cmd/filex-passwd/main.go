// filex-passwd generates a bcrypt password hash suitable for use in the
// filex config.yaml password_hash field.
//
// Usage:
//
//	filex-passwd [flags] [password]
//
// If no password argument is given, the password is read from the terminal
// without echo (recommended).
//
// Flags:
//
//	-config path   path to config.yaml; when combined with -user the hash is
//	               written directly into that file for the named user
//	-user   name   username whose password_hash to update in the config file
package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml (write hash to this file)")
	username := flag.String("user", "", "username whose password_hash to update")
	flag.Parse()

	var password string
	if flag.NArg() >= 1 {
		password = flag.Arg(0)
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

	hashStr := string(hash)

	// If both -config and -user are provided, write the hash into the config file.
	if *configPath != "" && *username != "" {
		if err := updateConfigHash(*configPath, *username, hashStr); err != nil {
			fmt.Fprintf(os.Stderr, "error updating config: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "password_hash for user %q updated in %s\n", *username, *configPath)
	} else if (*configPath != "" && *username == "") || (*configPath == "" && *username != "") {
		fmt.Fprintln(os.Stderr, "both -config and -user must be provided together")
		os.Exit(1)
	}

	// Always print the hash to stdout so it can be used in scripts.
	fmt.Println(hashStr)
}

// updateConfigHash reads the YAML config at path, finds the user with the
// given username, sets their password_hash to newHash, and writes the file
// back preserving comments and formatting as much as possible.
func updateConfigHash(path, username, newHash string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}

	// yaml.Unmarshal wraps the document in a DocumentNode.
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("unexpected YAML structure")
	}
	doc := root.Content[0]

	if err := setUserPasswordHash(doc, username, newHash); err != nil {
		return err
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}

	if err := os.WriteFile(path, out, info.Mode()); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

// setUserPasswordHash locates the users sequence in the mapping node doc,
// finds the entry whose username matches, and updates its password_hash value.
func setUserPasswordHash(doc *yaml.Node, username, newHash string) error {
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping at top level")
	}

	// Iterate key/value pairs in the top-level mapping.
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i]
		val := doc.Content[i+1]
		if key.Value != "users" {
			continue
		}
		if val.Kind != yaml.SequenceNode {
			return fmt.Errorf("'users' is not a sequence")
		}
		for _, userNode := range val.Content {
			if userNode.Kind != yaml.MappingNode {
				continue
			}
			if updated := trySetHash(userNode, username, newHash); updated {
				return nil
			}
		}
		return fmt.Errorf("user %q not found in config", username)
	}

	return fmt.Errorf("no 'users' section found in config")
}

// trySetHash looks for a username key matching target in a user mapping node
// and updates the password_hash value. Returns true if the user was found.
func trySetHash(userNode *yaml.Node, target, newHash string) bool {
	var foundUser bool
	var hashValueNode *yaml.Node

	for i := 0; i+1 < len(userNode.Content); i += 2 {
		k := userNode.Content[i]
		v := userNode.Content[i+1]
		switch k.Value {
		case "username":
			if v.Value == target {
				foundUser = true
			}
		case "password_hash":
			hashValueNode = v
		}
	}

	if foundUser && hashValueNode != nil {
		hashValueNode.Value = newHash
		return true
	}
	return false
}
