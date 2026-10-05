// Composes the explicit local interview demo without altering production gates.
// A verified offline page sample feeds Go BM25 and an optional local structured
// model adapter. Listener is literal loopback only; shutdown drains requests.
// No production snapshot, Qdrant/Neo4j mutation or deployment is performed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"regulagraph.local/server/internal/adapters/inference"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/answering"
	"regulagraph.local/server/internal/api"
	"regulagraph.local/server/internal/retrieval"
	"regulagraph.local/server/internal/workflows"
)

func runDemo(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(errOut)
	corpusPath := fs.String("corpus", "artifacts/interview-demo", "Verified offline demo page export")
	acquisitionPath := fs.String("acquisition", "data/acquisition", "Original D01 acquisition root")
	listen := fs.String("listen", "127.0.0.1:8096", "Literal loopback HTTP address")
	model := fs.String("model", "regulagraph-demo:latest", "Local Ollama model; empty means evidence only")
	endpoint := fs.String("endpoint", "http://127.0.0.1:11434", "Local OpenAI-compatible model base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || fs.NArg() != 0 {
		fmt.Fprintln(errOut, "demo requires a literal loopback listener")
		return 2
	}
	corpus, err := storage.LoadPreviewCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintln(errOut, "Load demo corpus:", err)
		return 1
	}
	index, err := retrieval.NewPreviewIndex(corpus.Pages)
	if err != nil {
		fmt.Fprintln(errOut, "Build demo index:", err)
		return 1
	}
	root, err := os.OpenRoot(*acquisitionPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer root.Close()
	var generator *answering.PreviewGenerator
	if *model != "" {
		provider, e := inference.NewOpenAICompatibleProvider(inference.OpenAICompatibleConfig{Endpoint: *endpoint, Timeout: 100 * time.Second, MaximumResponseBytes: 1 << 20})
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
		generator, e = answering.NewPreviewGenerator(provider, *model)
		if e != nil {
			fmt.Fprintln(errOut, e)
			return 2
		}
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	flow := workflows.NewPreview(index, generator)
	server := &http.Server{Handler: api.NewPreviewHandler(flow, corpus, root, index.PassageCount(), *model, listener.Addr().String()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = server.Shutdown(shutdown)
		case <-stopped:
		}
	}()
	fmt.Fprintf(out, "RegulaGraph local demo: http://%s\n%d documents, %d pages, %d passages; model=%s\n", listener.Addr(), len(corpus.PDFs), len(corpus.Pages), index.PassageCount(), *model)
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
