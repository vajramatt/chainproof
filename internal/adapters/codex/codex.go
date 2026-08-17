package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

const AdapterName = "codex-local-v1"

type Options struct {
	Root         string
	Content      string
	PollInterval time.Duration
}
type Stats struct {
	Sources        int `json:"sources"`
	RunsCreated    int `json:"runs_created"`
	EventsImported int `json:"events_imported"`
	Skipped        int `json:"skipped"`
	Errors         int `json:"errors"`
}
type Collector struct {
	store   *store.Store
	options Options
}

func New(s *store.Store, options Options) (*Collector, error) {
	if options.Root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		options.Root = filepath.Join(home, ".codex", "sessions")
	}
	if options.Content == "" {
		options.Content = "hashes"
	}
	if options.Content != "hashes" && options.Content != "full" {
		return nil, errors.New("Codex content mode must be hashes or full")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	return &Collector{store: s, options: options}, nil
}
func (c *Collector) Root() string { return c.options.Root }

func (c *Collector) Discover() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(c.options.Root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return fs.SkipDir
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	sort.Strings(paths)
	return paths, err
}

func (c *Collector) Sync(ctx context.Context) (Stats, error) {
	paths, err := c.Discover()
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Sources: len(paths)}
	var syncErrors []error
	for _, path := range paths {
		created, imported, skipped, err := c.syncFile(ctx, path)
		if err != nil {
			stats.Errors++
			syncErrors = append(syncErrors, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if created {
			stats.RunsCreated++
		}
		stats.EventsImported += imported
		stats.Skipped += skipped
	}
	return stats, errors.Join(syncErrors...)
}
func (c *Collector) Watch(ctx context.Context, onSync func(Stats, error)) {
	sync := func() {
		stats, err := c.Sync(ctx)
		if onSync != nil {
			onSync(stats, err)
		}
	}
	sync()
	ticker := time.NewTicker(c.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

func (c *Collector) syncFile(ctx context.Context, path string) (bool, int, int, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false, 0, 0, err
	}
	run, found, err := c.store.SourceRun(ctx, AdapterName, absolute)
	if err != nil {
		return false, 0, 0, err
	}
	created := false
	if !found {
		run, err = c.store.Start(ctx, agentFromPath(absolute), "codex", "", map[string]any{"source_path": absolute, "collection": "imported"})
		if err != nil {
			return false, 0, 0, err
		}
		if err = c.store.BindSourceRun(ctx, AdapterName, absolute, run.ID); err != nil {
			return false, 0, 0, err
		}
		created = true
	}
	cursor, err := c.store.Cursor(ctx, AdapterName, absolute)
	if err != nil {
		return created, 0, 0, err
	}
	offset := int64(0)
	if cursor != "" {
		offset, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return created, 0, 0, err
		}
	}
	file, err := os.Open(absolute)
	if err != nil {
		return created, 0, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return created, 0, 0, err
	}
	if info.Size() < offset {
		return created, 0, 0, errors.New("Codex session shrank")
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return created, 0, 0, err
	}
	reader := bufio.NewReaderSize(file, 256*1024)
	imported, skipped := 0, 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			input, metadata, ok, parseErr := c.normalize(absolute, offset, line)
			if parseErr != nil {
				return created, imported, skipped, fmt.Errorf("offset %d: %w", offset, parseErr)
			}
			if len(metadata) > 0 {
				agent, _ := metadata["agent"].(string)
				model, _ := metadata["model"].(string)
				delete(metadata, "agent")
				delete(metadata, "model")
				if err = c.store.UpdateRunIdentity(ctx, run.ID, agent, "codex", model, metadata); err != nil {
					return created, imported, skipped, err
				}
			}
			if ok {
				_, appendErr := c.store.Append(ctx, run.ID, input)
				if appendErr != nil {
					exists, existsErr := c.store.EventExists(ctx, input.ID)
					if existsErr != nil || !exists {
						return created, imported, skipped, appendErr
					}
				} else {
					imported++
				}
			} else {
				skipped++
			}
			offset += int64(len(line))
			if err = c.store.SetCursor(ctx, AdapterName, absolute, strconv.FormatInt(offset, 10)); err != nil {
				return created, imported, skipped, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return created, imported, skipped, readErr
		}
	}
	if time.Since(info.ModTime()) > 30*time.Second {
		if err = c.store.MarkIdle(ctx, run.ID); err != nil {
			return created, imported, skipped, err
		}
	}
	return created, imported, skipped, nil
}

func (c *Collector) normalize(path string, offset int64, line []byte) (proof.EventInput, map[string]any, bool, error) {
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		return proof.EventInput{}, nil, false, err
	}
	recordType, _ := record["type"].(string)
	payload, _ := record["payload"].(map[string]any)
	payloadType, _ := payload["type"].(string)
	timestamp := parseTime(record["timestamp"])
	nativeID := fmt.Sprintf("%s:%v:%d", recordType, record["ordinal"], offset)
	source := proof.Source{Adapter: AdapterName, Mode: "imported", NativeEventID: nativeID}
	input := proof.EventInput{ID: stableID(path, nativeID), Timestamp: timestamp, Source: source}
	metadata := map[string]any{}
	switch recordType {
	case "session_meta":
		cwd, _ := payload["cwd"].(string)
		metadata["agent"] = agentFromCWD(cwd)
		metadata["source_path"] = path
		metadata["cwd"] = cwd
		metadata["session_id"] = firstString(payload, "session_id", "id")
		metadata["cli_version"] = payload["cli_version"]
		metadata["model_provider"] = payload["model_provider"]
		metadata["originator"] = payload["originator"]
		metadata["collection"] = "imported"
		input.Kind = "session.started"
		input.Payload = map[string]any{"cwd": cwd, "session_id": metadata["session_id"], "cli_version": metadata["cli_version"], "originator": metadata["originator"]}
		return input, metadata, true, nil
	case "turn_context":
		metadata["agent"] = agentFromCWD(stringValue(payload["cwd"]))
		metadata["model"] = stringValue(payload["model"])
		metadata["source_path"] = path
		metadata["cwd"] = payload["cwd"]
		metadata["collection"] = "imported"
		input.Kind = "turn.context"
		input.Payload = selectKeys(payload, "turn_id", "model", "cwd", "approval_policy", "collaboration_mode", "timezone")
		return input, metadata, true, nil
	case "event_msg":
		switch payloadType {
		case "task_started":
			input.Kind = "turn.started"
			input.Payload = selectKeys(payload, "turn_id", "started_at", "collaboration_mode_kind", "model_context_window")
		case "task_complete":
			input.Kind = "turn.completed"
			input.Payload = selectKeys(payload, "turn_id", "started_at", "completed_at", "duration_ms", "time_to_first_token_ms")
		case "item_completed":
			return c.normalizeItem(input, payload)
		default:
			return proof.EventInput{}, nil, false, nil
		}
		return input, nil, true, nil
	default:
		return proof.EventInput{}, nil, false, nil
	}
}

func (c *Collector) normalizeItem(input proof.EventInput, payload map[string]any) (proof.EventInput, map[string]any, bool, error) {
	item, _ := payload["item"].(map[string]any)
	itemType, _ := item["type"].(string)
	base := map[string]any{"item_id": item["id"], "turn_id": payload["turn_id"]}
	switch itemType {
	case "UserMessage":
		input.Kind = "human.input"
		base["content"] = c.protect(item["content"])
	case "AgentMessage":
		input.Kind = "model.output"
		base["phase"] = item["phase"]
		base["content"] = c.protect(item["content"])
	case "CommandExecution":
		input.Kind = "tool.result"
		base["tool"] = "shell"
		base["command"] = c.protect(item["command"])
		base["cwd"] = item["cwd"]
		base["status"] = item["status"]
		base["exit_code"] = item["exit_code"]
		base["duration"] = item["duration"]
		base["stdout"] = c.protect(item["stdout"])
		base["stderr"] = c.protect(item["stderr"])
	case "FileChange":
		input.Kind = "artifact.changed"
		base["status"] = item["status"]
		base["changes"] = c.protect(item["changes"])
	case "Extension":
		input.Kind = "tool.result"
		base["tool"] = item["kind"]
		base["action"] = item["action"]
		base["query"] = c.protect(item["query"])
		base["results"] = c.protect(item["results"])
	case "ImageView":
		input.Kind = "tool.call"
		base["tool"] = "view_image"
		base["path"] = c.protect(item["path"])
	case "Reasoning":
		return proof.EventInput{}, nil, false, nil
	default:
		return proof.EventInput{}, nil, false, nil
	}
	input.Payload = base
	return input, nil, true, nil
}

func (c *Collector) protect(value any) any {
	if c.options.Content == "full" {
		return value
	}
	raw, _ := proof.CanonicalJSON(value)
	sum := sha256.Sum256(raw)
	return map[string]any{"sha256": hex.EncodeToString(sum[:]), "bytes": len(raw)}
}
func stableID(path, native string) string {
	sum := sha256.Sum256([]byte(path + "\x00" + native))
	return "codex-" + hex.EncodeToString(sum[:])
}
func parseTime(value any) *time.Time {
	s, ok := value.(string)
	if !ok {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}
func stringValue(value any) string { s, _ := value.(string); return s }
func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := stringValue(values[key]); s != "" {
			return s
		}
	}
	return ""
}
func agentFromCWD(cwd string) string {
	if cwd == "" {
		return "codex"
	}
	return filepath.Base(cwd)
}
func agentFromPath(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if len(name) > 28 {
		name = name[len(name)-28:]
	}
	return "codex-" + name
}
func selectKeys(source map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if value, ok := source[key]; ok {
			out[key] = value
		}
	}
	return out
}
