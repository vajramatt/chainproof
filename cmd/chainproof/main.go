package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ChainProofAI/chainproof/internal/proof"
	"github.com/ChainProofAI/chainproof/internal/server"
	"github.com/ChainProofAI/chainproof/internal/store"
	"github.com/ChainProofAI/chainproof/internal/tui"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "chainproof:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		args = []string{"ui"}
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Println("chainproof", version)
		return nil
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}
	if args[0] == "verify-file" {
		if len(args) < 2 {
			return errors.New("usage: chainproof verify-file PROOF.json")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var bundle proof.Bundle
		if err = json.Unmarshal(raw, &bundle); err != nil {
			return err
		}
		verification := proof.VerifyBundle(bundle)
		_ = output(verification, nil)
		if !verification.Valid {
			return errors.New("verification failed")
		}
		return nil
	}
	dbPath := os.Getenv("CHAINPROOF_DB")
	if dbPath == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		dbPath = filepath.Join(home, ".chainproof", "chainproof.db")
	}
	if e := os.MkdirAll(filepath.Dir(dbPath), 0700); e != nil {
		return e
	}
	db, e := store.Open(dbPath)
	if e != nil {
		return e
	}
	defer db.Close()
	ctx := context.Background()
	switch args[0] {
	case "init":
		fmt.Println("initialized", dbPath)
		return nil
	case "start":
		fs := flag.NewFlagSet("start", flag.ContinueOnError)
		agent := fs.String("agent", "unknown-agent", "")
		harness := fs.String("harness", "", "")
		model := fs.String("model", "", "")
		if e = fs.Parse(args[1:]); e != nil {
			return e
		}
		r, e := db.Start(ctx, *agent, *harness, *model, nil)
		return output(r, e)
	case "append":
		if len(args) < 2 {
			return errors.New("usage: chainproof append RUN_ID [JSON]")
		}
		var in proof.EventInput
		raw := strings.Join(args[2:], " ")
		if raw == "" {
			b, _ := io.ReadAll(os.Stdin)
			raw = string(b)
		}
		if e = json.Unmarshal([]byte(raw), &in); e != nil {
			return e
		}
		v, e := db.Append(ctx, args[1], in)
		return output(v, e)
	case "ingest":
		if len(args) < 2 {
			return errors.New("usage: chainproof ingest RUN_ID < events.jsonl")
		}
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			var in proof.EventInput
			if e = json.Unmarshal(scan.Bytes(), &in); e != nil {
				return e
			}
			if in.Source.Mode == "" {
				in.Source.Mode = "imported"
			}
			if _, e = db.Append(ctx, args[1], in); e != nil {
				return e
			}
		}
		return scan.Err()
	case "pull":
		if len(args) < 3 {
			return errors.New("usage: chainproof pull RUN_ID EVENTS.jsonl [ADAPTER]")
		}
		adapter := "jsonl-file"
		if len(args) > 3 {
			adapter = args[3]
		}
		return pullFile(ctx, db, args[1], args[2], adapter)
	case "complete":
		if len(args) < 2 {
			return errors.New("usage: chainproof complete RUN_ID [completed|failed|cancelled]")
		}
		status := "completed"
		if len(args) > 2 {
			status = args[2]
		}
		v, e := db.Complete(ctx, args[1], status)
		return output(v, e)
	case "verify":
		if len(args) < 2 {
			return errors.New("usage: chainproof verify RUN_ID")
		}
		v := db.Verify(ctx, args[1])
		output(v, nil)
		if !v.Valid {
			return errors.New("verification failed")
		}
		return nil
	case "export":
		if len(args) < 2 {
			return errors.New("usage: chainproof export RUN_ID [PROOF.json]")
		}
		bundle, e := db.Bundle(ctx, args[1])
		if e != nil {
			return e
		}
		if len(args) < 3 {
			return output(bundle, nil)
		}
		raw, e := json.MarshalIndent(bundle, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(args[2], append(raw, '\n'), 0600); e != nil {
			return e
		}
		fmt.Println(args[2])
		return nil
	case "list":
		v, e := db.Runs(ctx, 100)
		return output(v, e)
	case "ui":
		return tui.Run(db)
	case "serve":
		address := "127.0.0.1:7331"
		if len(args) > 1 {
			address = args[1]
		}
		fmt.Printf("ChainProof web explorer: http://%s\n", address)
		e = server.New(db, address).ListenAndServe()
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case "run":
		if len(args) < 2 {
			return errors.New("usage: chainproof run -- COMMAND [ARGS...]")
		}
		command := args[1:]
		if command[0] == "--" {
			command = command[1:]
		}
		if len(command) == 0 {
			return errors.New("usage: chainproof run -- COMMAND [ARGS...]")
		}
		r, e := db.Start(ctx, filepath.Base(command[0]), filepath.Base(command[0]), "", map[string]any{"command": command})
		if e != nil {
			return e
		}
		db.Append(ctx, r.ID, proof.EventInput{Kind: "run.started", Source: proof.Source{Adapter: "command-wrapper", Mode: "observed"}, Payload: map[string]any{"command": command}})
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		e = cmd.Run()
		status := "completed"
		code := 0
		if e != nil {
			status = "failed"
			if exit := new(exec.ExitError); errors.As(e, &exit) {
				code = exit.ExitCode()
			} else {
				code = 1
			}
		}
		db.Append(ctx, r.ID, proof.EventInput{Kind: "run.completed", Source: proof.Source{Adapter: "command-wrapper", Mode: "observed"}, Payload: map[string]any{"exit_code": code}})
		db.Complete(ctx, r.ID, status)
		fmt.Fprintln(os.Stderr, "ChainProof run:", r.ID)
		return e
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

const usage = `ChainProof — local provenance for any AI agent

Usage:
  chainproof ui                              Open the terminal interface
  chainproof serve [127.0.0.1:7331]         Run local API and web dashboard
  chainproof start [--agent A --harness H --model M]
  chainproof append RUN_ID [JSON]            Append one reported event
  chainproof ingest RUN_ID < events.jsonl    Import a JSONL stream
  chainproof pull RUN_ID FILE [ADAPTER]      Pull new JSONL records by cursor
  chainproof run -- COMMAND [ARGS...]        Wrap any agent harness
  chainproof complete RUN_ID [STATUS]
  chainproof verify RUN_ID                   Verify the local ledger
  chainproof export RUN_ID [PROOF.json]      Export a portable proof
  chainproof verify-file PROOF.json          Verify without a database
  chainproof list
  chainproof version
`

func pullFile(ctx context.Context, db *store.Store, runID, path, adapter string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	cursor, err := db.Cursor(ctx, adapter, abs)
	if err != nil {
		return err
	}
	offset := int64(0)
	if cursor != "" {
		offset, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid stored cursor: %w", err)
		}
	}
	file, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < offset {
		return errors.New("source file shrank; use a new adapter name to re-import")
	}
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	reader, count := bufio.NewReader(file), 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var in proof.EventInput
			if err = json.Unmarshal(line, &in); err != nil {
				return fmt.Errorf("offset %d: %w", offset, err)
			}
			in.Source.Mode = "imported"
			in.Source.Adapter = adapter
			in.Source.NativeEventID = fmt.Sprintf("%s:%d", abs, offset)
			if _, err = db.Append(ctx, runID, in); err != nil {
				return err
			}
			offset += int64(len(line))
			if err = db.SetCursor(ctx, adapter, abs, strconv.FormatInt(offset, 10)); err != nil {
				return err
			}
			count++
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	fmt.Printf("imported %d events; cursor %d\n", count, offset)
	return nil
}
func output(v any, e error) error {
	if e != nil {
		return e
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
