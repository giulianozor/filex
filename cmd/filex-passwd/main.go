// filex-passwd generates a bcrypt password hash suitable for the password_hash
// field of the user preferences file (~/.config/filex.yaml).
//
// Usage:
//
//	filex-passwd [flags] [password]
//
// If no password argument is given, the password is read without echo from an
// interactive terminal, or from a single line piped on stdin when stdin is not
// a terminal (e.g. `echo -n s3cret | filex-passwd -user bob`).
//
// Flags:
//
//	-user   name   username whose password_hash to update in the preferences file
//	-prefs  path   path to the user preferences file (default the user's own ~/.config/filex.yaml)
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/giulianozor/filex/internal/atomicfile"
	"github.com/giulianozor/filex/internal/prefs"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// bcryptCost is deliberately above bcrypt.DefaultCost (10): password hashes
// live in a readable config file and are the only credential, so the extra
// work factor is cheap insurance against offline cracking.
const bcryptCost = 12

func main() {
	configPath := flag.String("prefs", "", "path to the user preferences file (default the user's own ~/.config/filex.yaml)")
	username := flag.String("user", "", "username whose password_hash to update")
	flag.Parse()

	if flag.NArg() > 1 {
		fail("usage: filex-passwd [flags] [password]")
	}

	var password string
	if flag.NArg() == 1 {
		password = flag.Arg(0)
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Non-interactive stdin (a pipe): read a single line so scripts can
		// pipe the password in without hanging on the hidden-input prompt and
		// without embedding any trailing lines in the password.
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			fail("error reading password: %v", err)
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		fmt.Fprint(os.Stderr, "Password: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr) // newline after the hidden input
		if err != nil {
			fail("error reading password: %v", err)
		}
		password = string(raw)
	}

	if password == "" {
		fail("password must not be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		fail("error generating hash: %v", err)
	}

	hashStr := string(hash)

	// Writing a hash requires a username. The -prefs path defaults to that user's
	// own <home>/.config/filex.yaml (resolved from the account database) so
	// `filex-passwd -user alice` just works and the hash lands where the server
	// will read it; pass -prefs to target a different file, e.g. on a container
	// without a passwd entry for the user. A -prefs value without -user is a
	// tell-tale mistake, so surface it instead of silently doing nothing.
	if *username != "" {
		prefsPath := *configPath
		if prefsPath == "" {
			var err error
			prefsPath, err = prefs.PathForUser(*username, nil)
			if err != nil {
				fail("cannot resolve preferences path for user %q: %v", *username, err)
			}
		}
		if err := updatePrefsHash(prefsPath, *username, hashStr); err != nil {
			fail("error updating preferences: %v", err)
		}
		fmt.Fprintf(os.Stderr, "password_hash for user %q updated in %s\n", *username, prefsPath)
	} else if *configPath != "" {
		fail("both -user and -prefs must be provided together")
	}

	// Always print the hash to stdout so it can be used in scripts.
	fmt.Println(hashStr)
}

// fail prints a message to stderr and exits with a non-zero status.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// updatePrefsHash reads the YAML preferences file at path, finds the user with
// the given username, sets their password_hash to newHash, and writes the file
// back preserving comments and formatting as much as possible. The file has the
// same top-level shape as config.yaml for the users section, so the node-based
// editor is shared. A missing or empty file is bootstrapped into a minimal
// document instead of being treated as corrupt (e.g. a fresh `touch prefs.yaml`
// in Docker).
func updatePrefsHash(path, username, newHash string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading preferences: %w", err)
	}

	mode := os.FileMode(0o600)
	if err == nil {
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	}

	var out []byte
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		// Missing or empty preferences file: bootstrap the document from the
		// prefs types so the hash can be attached on first run.
		doc := prefs.Preferences{Users: []prefs.User{{Username: username, PasswordHash: newHash}}}
		out, err = yaml.Marshal(doc)
		if err != nil {
			return fmt.Errorf("marshalling preferences: %w", err)
		}
	} else {
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing preferences: %w", err)
		}
		// yaml.Unmarshal wraps the document in a DocumentNode.
		if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
			return fmt.Errorf("unexpected YAML structure")
		}
		if err := setUserPasswordHash(root.Content[0], username, newHash); err != nil {
			return err
		}
		out, err = yaml.Marshal(&root)
		if err != nil {
			return fmt.Errorf("marshalling preferences: %w", err)
		}
	}

	// Write through the shared atomic writer (unique temp file next to the
	// target + rename) so a crash mid-write cannot truncate the preferences.
	// Create the parent directory too: a fresh machine usually has no ~/.config
	// yet, and without it the temp-file write below fails with a missing dir.
	// The file is chowned to the account user's uid/gid so a root-run tool does
	// not leave the user's own preferences owned by root (a user with no passwd
	// entry - e.g. a container - resolves to -1/-1 and the file stays owned by
	// whoever ran the tool).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating preferences directory: %w", err)
	}
	uid, gid := prefs.OwnerForUser(username, nil, nil)
	if err := atomicfile.WriteOwned(path, out, mode, uid, gid); err != nil {
		return fmt.Errorf("writing preferences: %w", err)
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
		return fmt.Errorf("user %q not found in preferences", username)
	}

	return fmt.Errorf("no 'users' section found in preferences")
}

// trySetHash looks for a username key matching target in a user mapping node
// and updates the password_hash value. A user in the config may have no
// password_hash key yet; in that case the key is appended. Returns true if the
// user was found.
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

	if !foundUser {
		return false
	}
	if hashValueNode == nil {
		userNode.Content = append(userNode.Content, scalarNode("password_hash"), scalarNode(newHash))
		return true
	}
	hashValueNode.Value = newHash
	return true
}

// scalarNode builds a short string node for the YAML key/value pair with
// single quoting, keeping the "2a" prefix of a bcrypt hash unambiguous.
func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.SingleQuotedStyle}
}
