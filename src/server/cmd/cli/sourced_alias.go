// Exposes inspect/accept for existing identities or explicit provisional creation.
// CLI owns local OS principal/configuration only; workflows hydrate evidence and
// registry storage atomically records reviewed aliases. No implicit acceptance,
// automatic identity equivalence or model call. Deadlines bound IO; output includes source
// text for human review. Benchmark targets remain required and unmeasured.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/config"
	"regulagraph.local/server/internal/workflows"
)

type sourcedAliasOptions struct {
	CreateProvisional                                                                         bool
	Action, Source, TargetDocument, Mention, Canonical, Scope, Label, Operation, Hash, Reason string
	Revision                                                                                  uint64
	Timeout                                                                                   time.Duration
}

func runSourcedAlias(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runSourcedAliasWith(ctx, args, out, errOut, executeSourcedAlias)
}

func runSourcedAliasWith(ctx context.Context, args []string, out, errOut io.Writer, execute func(context.Context, sourcedAliasOptions) (any, error)) int {
	fs := flag.NewFlagSet("review-alias", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o sourcedAliasOptions
	fs.StringVar(&o.Action, "action", "inspect", "inspect or accept an explicitly reviewed alias plan")
	fs.BoolVar(&o.CreateProvisional, "create-provisional", false, "Create an UNREVIEWED source-occurrence identity; forbids canonical and target-document")
	fs.StringVar(&o.Source, "source", "", "Registered successful EXTRACT artifact ID")
	fs.StringVar(&o.TargetDocument, "target-document", "", "Registered BIND/CHUNK DocumentBatch proving the selected target identity")
	fs.StringVar(&o.Mention, "mention", "", "Exact EXTRACT support mention ID")
	fs.StringVar(&o.Canonical, "canonical", "", "Existing canonical ID explicitly selected by the operator")
	fs.StringVar(&o.Scope, "scope", "", "Canonical/alias scope allowed by pinned candidate policy")
	fs.StringVar(&o.Label, "label", "", "Preferred canonical label; must match the immutable profile when one exists")
	fs.Uint64Var(&o.Revision, "expected-revision", 0, "Accept requires the inspected registry revision")
	fs.StringVar(&o.Operation, "operation", "", "Accept requires a stable unique operation key for replay")
	fs.StringVar(&o.Hash, "plan-sha256", "", "Accept requires the inspected alias plan hash")
	fs.StringVar(&o.Reason, "reason", "", "Accept requires explicit human review rationale")
	fs.DurationVar(&o.Timeout, "timeout", 30*time.Second, "Total deadline, at most 5m")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || o.Source == "" || (o.CreateProvisional && (o.Canonical != "" || o.TargetDocument != "")) || (!o.CreateProvisional && (o.TargetDocument == "" || o.Canonical == "")) || o.Mention == "" || o.Scope == "" || o.Label == "" || o.Timeout <= 0 || o.Timeout > 5*time.Minute ||
		(o.Action != "inspect" && o.Action != "accept") || (o.Action == "accept" && (o.Revision == 0 || o.Operation == "" || len(o.Hash) != 64 || o.Reason == "")) ||
		(o.Action == "inspect" && (o.Revision != 0 || o.Operation != "" || o.Hash != "" || o.Reason != "")) {
		fmt.Fprintln(errOut, "Invalid alias review selection, limits or acceptance pins")
		return 2
	}
	bounded, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	result, err := execute(bounded, o)
	if err != nil {
		fmt.Fprintln(errOut, "Alias review failed: check source/target evidence, policy, profile and revision. No successful registration is implied.")
		return 1
	}
	if err = json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "Could not write result; retry the exact acceptance to reconcile committed state.")
		return 1
	}
	return 0
}

func executeSourcedAlias(ctx context.Context, o sourcedAliasOptions) (any, error) {
	actor, err := localReviewActor()
	if err != nil {
		return nil, err
	}
	corpus, scope := os.Getenv("REGULAGRAPH_REVIEW_CORPUS_ID"), os.Getenv("REGULAGRAPH_COORDINATOR_AUTH_SCOPE")
	if corpus == "" || scope == "" || os.Getenv("REGULAGRAPH_POSTGRES_DSN") == "" || os.Getenv("REGULAGRAPH_ARTIFACTS_DIR") == "" {
		return nil, errors.New("explicit local operator configuration required")
	}
	policies, err := config.LoadCandidatePlanningPolicies(os.Getenv("REGULAGRAPH_CANDIDATE_POLICY_PATH"), os.Getenv("REGULAGRAPH_CANDIDATE_POLICY_SHA256"))
	if err != nil {
		return nil, err
	}
	policy, ok := policies[corpus]
	if !ok {
		return nil, errors.New("corpus policy missing")
	}
	repo, err := postgres.Open(ctx, postgres.Config{DSN: os.Getenv("REGULAGRAPH_POSTGRES_DSN"), MaxConnections: 2, HealthTimeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	defer repo.Close()
	files, err := storage.NewFileStore(os.Getenv("REGULAGRAPH_ARTIFACTS_DIR"))
	if err != nil {
		return nil, err
	}
	defer files.Close()
	view, err := workflows.InspectSourcedAlias(ctx, repo, files, workflows.SourcedAliasOptions{CreateProvisional: o.CreateProvisional, Corpus: corpus, AuthScope: scope, SourceArtifactID: o.Source, TargetDocumentID: o.TargetDocument,
		MentionID: o.Mention, CanonicalID: o.Canonical, Scope: o.Scope, PreferredLabel: o.Label, Revision: o.Revision, Policy: policy})
	if err != nil {
		return nil, err
	}
	if o.Action == "accept" {
		revision, err := view.Accept(ctx, repo, o.Operation, o.Hash, actor, o.Reason)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "alias_registered", "registry_revision": revision, "operation": o.Operation, "plan_sha256": o.Hash, "actor": actor, "graph_published": false, "create_provisional": o.CreateProvisional}, nil
	}
	p, err := view.Preview()
	if err != nil {
		return nil, err
	}
	result := map[string]any{"status": "inspection_only", "plan_sha256": p.PlanHash, "expected_revision": p.ExpectedRevision, "actor": actor}
	result["preferred_label_is_operator_supplied"] = true
	result["create_provisional"] = p.CreateProvisional
	if p.CreateProvisional {
		result["identity_basis"] = "source_occurrence_only"
		result["target_document_role"] = "source_bind_inventory"
	}
	for name, msg := range map[string]proto.Message{"entity": p.Registration.Entity, "alias": p.Registration.Alias, "mention": p.Mention, "source_context": p.SourceContext, "target_document": p.TargetDocument, "target_document_ref": p.TargetDocumentRef} {
		raw, e := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(msg)
		if e != nil {
			return nil, e
		}
		result[name] = json.RawMessage(raw)
	}
	contexts := make([]json.RawMessage, 0, len(p.Contexts))
	for _, c := range p.Contexts {
		raw, e := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(c)
		if e != nil {
			return nil, e
		}
		contexts = append(contexts, json.RawMessage(raw))
	}
	result["contexts"] = contexts
	return result, nil
}
