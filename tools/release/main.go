// Command release does the parts of a release that GitHub Actions can't: the next beta's version
// and notes, and the update feed the apps read. Run from the repository root:
//
//	go run ./tools/release beta-version   # the next beta, e.g. 1.1.0-beta.2 (from git)
//	go run ./tools/release notes          # its release notes (markdown, commits since the last release)
//	go run ./tools/release feed ...       # adds a release to releases.json and writes appcast.xml
//	go run ./tools/release keygen         # a new update signing key pair (once)
//
// The workflows in .github/workflows (release-beta.yml, release-stable.yml) run it.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: release beta-version | notes | feed | keygen"))
	}
	var err error
	switch os.Args[1] {
	case "beta-version":
		err = betaVersion()
	case "notes":
		err = releaseNotes(os.Args[2:])
	case "feed":
		err = updateFeed(os.Args[2:])
	case "keygen":
		err = keygen()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "release:", err)
	os.Exit(1)
}

// sinceStable is the last stable release the checked-out commit contains and the commits after it
// (merges left out: their commits count on their own).
func sinceStable() (*version, []commit, error) {
	merged, err := git("tag", "--merged", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	last := lastStable(strings.Fields(merged))
	rng := "HEAD"
	if last != nil {
		rng = "v" + last.String() + "..HEAD"
	}
	// Each commit as hash, then message, ended by a NUL byte.
	log, err := git("log", "--no-merges", "--format=%H%n%B%x00", rng)
	if err != nil {
		return nil, nil, err
	}
	var commits []commit
	for _, entry := range strings.Split(log, "\x00") {
		hash, message, _ := strings.Cut(strings.TrimSpace(entry), "\n")
		if hash != "" {
			commits = append(commits, commit{hash, strings.TrimSpace(message)})
		}
	}
	return last, commits, nil
}

func betaVersion() error {
	last, commits, err := sinceStable()
	if err != nil {
		return err
	}
	tags, err := git("tag")
	if err != nil {
		return err
	}
	next, err := nextBeta(last, commits, strings.Fields(tags))
	if err != nil {
		return err
	}
	fmt.Println(next)
	return nil
}

func releaseNotes(args []string) error {
	flags := flag.NewFlagSet("notes", flag.ExitOnError)
	repo := flags.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/name, for commit links")
	_ = flags.Parse(args)

	_, commits, err := sinceStable()
	if err != nil {
		return err
	}
	fmt.Print(notes(*repo, commits))
	return nil
}

// updateFeed adds a release, built and uploaded already, to releases.json and writes appcast.xml
// next to it.
func updateFeed(args []string) error {
	flags := flag.NewFlagSet("feed", flag.ExitOnError)
	repo := flags.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/name the release is on")
	ver := flags.String("version", "", "the release, e.g. 1.1.0 or 1.1.0-beta.2")
	macOS := flags.String("macos", "", "the macOS disk image")
	windows := flags.String("windows", "", "the Windows installer")
	notesHTML := flags.String("notes-html", "", "the release notes, as HTML")
	dir := flags.String("dir", ".", "where releases.json is read from (if there) and both files are written")
	_ = flags.Parse(args)

	v, ok := parseVersion(*ver)
	if !ok || *repo == "" || *macOS == "" || *windows == "" {
		return errors.New("feed needs -version, -repo, -macos and -windows")
	}
	key := os.Getenv("SPARKLE_PRIVATE_KEY")
	if key == "" {
		return errors.New("SPARKLE_PRIVATE_KEY is not set")
	}

	path := filepath.Join(*dir, "releases.json")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := parseFeed(data)
	if err != nil {
		return err
	}

	r := release{
		Version:  v.String(),
		Date:     time.Now().UTC().Truncate(time.Second),
		NotesURL: fmt.Sprintf("https://github.com/%s/releases/tag/v%s", *repo, v),
	}
	if *notesHTML != "" {
		html, err := os.ReadFile(*notesHTML)
		if err != nil {
			return err
		}
		r.NotesHTML = string(html)
	}
	if r.MacOS, err = describe(*repo, v, *macOS, minimumMacOS, key); err != nil {
		return err
	}
	if r.Windows, err = describe(*repo, v, *windows, minimumWindows, ""); err != nil {
		return err
	}
	f.add(r)

	out, err := f.json()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	cast, err := f.appcast()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*dir, "appcast.xml"), cast, 0o644)
}

// describe is a download of the release: its address on GitHub, size and hashes. key, when given,
// signs it for Sparkle.
func describe(repo string, v version, path, minimum, key string) (*download, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	d := &download{
		URL:                  fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", repo, v, filepath.Base(path)),
		Length:               int64(len(data)),
		SHA256:               hex.EncodeToString(sum[:]),
		MinimumSystemVersion: minimum,
	}
	if key != "" {
		if d.EdSignature, err = sign(data, key); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func keygen() error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Println("Secret SPARKLE_PRIVATE_KEY (signs updates; keep it only in GitHub):")
	fmt.Println(base64.StdEncoding.EncodeToString(private.Seed()))
	fmt.Println()
	fmt.Println("Variable SPARKLE_PUBLIC_KEY (built into the macOS app as SUPublicEDKey):")
	fmt.Println(base64.StdEncoding.EncodeToString(public))
	return nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}
