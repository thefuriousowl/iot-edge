package dbmaintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type BackupMetadata struct {
	Format     int    `json:"format"`
	Database   string `json:"database"`
	CreatedAt  string `json:"created_at"`
	SHA256     string `json:"sha256"`
	DumpFormat string `json:"dump_format"`
}

func Backup(ctx context.Context, databaseURL, pgDump, output, expectedDatabase string) (BackupMetadata, error) {
	if !filepath.IsAbs(output) || filepath.Ext(output) != ".dump" {
		return BackupMetadata{}, errors.New("backup output must be an absolute .dump path")
	}
	publicURL, password, databaseName, err := commandConnection(databaseURL)
	if err != nil {
		return BackupMetadata{}, err
	}
	defer clear(password)
	if databaseName != expectedDatabase {
		return BackupMetadata{}, errors.New("connected database identity changed")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return BackupMetadata{}, fmt.Errorf("create backup directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".iot-edge-backup-*.tmp")
	if err != nil {
		return BackupMetadata{}, fmt.Errorf("create temporary backup: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return BackupMetadata{}, err
	}
	defer os.Remove(temporaryPath)
	if err := runPostgresTool(ctx, pgDump, password, "--format=custom", "--no-owner", "--no-privileges", "--file", temporaryPath, "--dbname", publicURL); err != nil {
		return BackupMetadata{}, fmt.Errorf("create PostgreSQL backup: %w", err)
	}
	digest, err := fileSHA256(temporaryPath)
	if err != nil {
		return BackupMetadata{}, err
	}
	metadata := BackupMetadata{Format: 1, Database: databaseName, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), SHA256: digest, DumpFormat: "postgresql-custom"}
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return BackupMetadata{}, err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return BackupMetadata{}, fmt.Errorf("publish backup: %w", err)
	}
	if err := os.WriteFile(output+".json", append(encoded, '\n'), 0o600); err != nil {
		_ = os.Remove(output)
		return BackupMetadata{}, fmt.Errorf("write backup metadata: %w", err)
	}
	return metadata, nil
}

func VerifyBackup(path, expectedDatabase string) (BackupMetadata, error) {
	if !filepath.IsAbs(path) {
		return BackupMetadata{}, errors.New("backup path must be absolute")
	}
	raw, err := readBounded(path+".json", 16<<10)
	if err != nil {
		return BackupMetadata{}, fmt.Errorf("read backup metadata: %w", err)
	}
	var metadata BackupMetadata
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil || metadata.Format != 1 || metadata.Database != expectedDatabase || metadata.DumpFormat != "postgresql-custom" || len(metadata.SHA256) != 64 {
		return BackupMetadata{}, errors.New("backup metadata is invalid or targets another database")
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return BackupMetadata{}, err
	}
	if digest != metadata.SHA256 {
		return BackupMetadata{}, errors.New("backup checksum mismatch")
	}
	return metadata, nil
}

func Restore(ctx context.Context, databaseURL, pgRestore, input, expectedDatabase string) error {
	if _, err := VerifyBackup(input, expectedDatabase); err != nil {
		return err
	}
	publicURL, password, databaseName, err := commandConnection(databaseURL)
	if err != nil {
		return err
	}
	defer clear(password)
	if databaseName != expectedDatabase {
		return errors.New("connected database identity changed")
	}
	return runPostgresTool(ctx, pgRestore, password, "--clean", "--if-exists", "--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname", publicURL, input)
}

func commandConnection(raw string) (string, []byte, string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil {
		return "", nil, "", errors.New("database URL is invalid")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	databaseName, err = url.PathUnescape(databaseName)
	if err != nil || databaseName == "" || strings.Contains(databaseName, "/") {
		return "", nil, "", errors.New("database URL must name exactly one database")
	}
	passwordText, _ := parsed.User.Password()
	password := []byte(passwordText)
	parsed.User = url.User(parsed.User.Username())
	return parsed.String(), password, databaseName, nil
}

func runPostgresTool(ctx context.Context, executable string, password []byte, arguments ...string) error {
	if !filepath.IsAbs(executable) {
		return errors.New("PostgreSQL tool path must be absolute")
	}
	toolName := filepath.Base(executable)
	if !strings.EqualFold(toolName, "pg_dump.exe") && !strings.EqualFold(toolName, "pg_restore.exe") {
		return errors.New("only pg_dump.exe and pg_restore.exe are allowed")
	}
	// #nosec G204 -- executable is absolute and its basename is restricted to
	// the two PostgreSQL maintenance programs immediately above.
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = append(os.Environ(), "PGPASSWORD="+string(password))
	var output boundedBuffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("PostgreSQL tool failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("open backup root: %w", err)
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 64<<30)); err != nil {
		return "", fmt.Errorf("hash backup: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return contents, nil
}

func Within(path, root string) bool {
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type boundedBuffer struct{ bytes.Buffer }

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	if buffer.Len() < 16<<10 {
		remaining := (16 << 10) - buffer.Len()
		_, _ = buffer.Buffer.Write(value[:min(len(value), remaining)])
	}
	return written, nil
}
