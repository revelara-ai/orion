package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/modelfetch"
)

// cmdModel implements `orion model fetch [--dir]` (or-c6zf.5): provision the
// opt-in semantic-memory embedder's assets (bge-base-en-v1.5 ONNX +
// tokenizer, MIT-licensed by BAAI) with pinned SHA-256 verification.
// Idempotent: matching checksums skip; corruption re-downloads.
func cmdModel(args []string) int {
	if len(args) == 0 || args[0] != "fetch" {
		fmt.Fprintln(os.Stderr, "orion model: expected 'fetch' (downloads the semantic-memory embedder model — see docs/semantic-memory.md)")
		return 2
	}
	fs := flag.NewFlagSet("model fetch", flag.ContinueOnError)
	dir := fs.String("dir", "", "target dir for model.onnx + tokenizer.json (default: <data-dir>/models)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	target := *dir
	if target == "" {
		dd, err := resolveDataDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion model fetch:", err)
			return 1
		}
		target = filepath.Join(dd, "models")
	}
	fmt.Printf("provisioning bge-base-en-v1.5 into %s (MIT license, BAAI — https://huggingface.co/BAAI/bge-base-en-v1.5)\n", target)
	results, err := modelfetch.Fetch(context.Background(), target, modelfetch.BGEBaseAssets(), func(line string) { fmt.Println("  " + line) })
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion model fetch:", err)
		return 1
	}
	fetched := 0
	for _, r := range results {
		if !r.Skipped {
			fetched++
		}
	}
	fmt.Printf("done: %d fetched, %d already present\nsemantic recall is now ON by default (no env needed; opt out with ORION_MEMORY_EMBEDDER=off)\n", fetched, len(results)-fetched)
	return 0
}

// embedderCheck (or-c6zf.5, or-o213): the doctor's semantic-recall probe.
// Semantic recall is opt-OUT: on by default once provisioned (`orion model
// fetch`), so the states are — explicitly disabled (ok), on (ok), not yet
// provisioned (ok with the enable hint), or explicitly configured but broken
// (warn: recall silently degrades to keyword+heat, say so).
func embedderCheck(dataDir string) doctorCheck {
	env := os.Getenv("ORION_MEMORY_EMBEDDER")
	switch env {
	case "off", "none", "0":
		return doctorCheck{Name: "embedder", Status: statusOK, Detail: "semantic recall disabled (ORION_MEMORY_EMBEDDER=" + env + ")"}
	case "":
		if dataDir == "" {
			return doctorCheck{Name: "embedder", Status: statusOK, Detail: "semantic recall not provisioned (no data dir)"}
		}
		dir := filepath.Join(dataDir, "models")
		if ok, _ := modelfetch.VerifyQuick(dir, modelfetch.BGEBaseAssets()); !ok {
			return doctorCheck{Name: "embedder", Status: statusOK, Detail: "semantic recall not provisioned (keyword+heat recall) — run `orion model fetch` to enable it"}
		}
		return doctorCheck{Name: "embedder", Status: statusOK, Detail: "semantic recall on (default) — assets present (" + dir + ")"}
	}
	dir := os.Getenv("ORION_MEMORY_MODEL_PATH")
	if dir == "" && dataDir != "" {
		dir = filepath.Join(dataDir, "models")
	}
	if ok, detail := modelfetch.VerifyQuick(dir, modelfetch.BGEBaseAssets()); !ok {
		return doctorCheck{Name: "embedder", Status: statusWarn, Detail: detail + " under " + dir + " — semantic recall degraded to keyword+heat; run `orion model fetch`"}
	}
	return doctorCheck{Name: "embedder", Status: statusOK, Detail: "embedder assets present (" + dir + ")"}
}
