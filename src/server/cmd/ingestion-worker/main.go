// Runs the durable Go ingestion coordinator against PostgreSQL and the loopback Rust PARSE worker.
//
// Configuration is loaded explicitly from REGULAGRAPH_* environment variables. Startup opens each
// dependency once; the loop claims bounded jobs, emits JSON operational events, and drains through signal
// cancellation. Migrations and snapshot publication remain separate operational stages. Measure queue and
// stage p95/p99 plus retry/cancellation behavior against configs/benchmark-targets.yaml.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc/credentials/insecure"
	"regulagraph.local/server/internal/adapters/postgres"
	workeradapter "regulagraph.local/server/internal/adapters/worker"
	"regulagraph.local/server/internal/workflows"
)

type runtimeConfig struct {
	postgresDSN      string
	workerEndpoint   string
	ownerID          string
	authScope        string
	lease            time.Duration
	callTimeout      time.Duration
	cancellationPoll time.Duration
	idlePoll         time.Duration
	retryBase        time.Duration
	retryMax         time.Duration
	maxMessageBytes  int
}

var coordinatorIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"level": "error", "component": "ingestion-worker", "error": err.Error()})
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	repository, err := postgres.Open(ctx, postgres.Config{
		DSN: config.postgresDSN, MaxConnections: 8, MinConnections: 1,
		ConnectTimeout: 5 * time.Second, HealthTimeout: 5 * time.Second,
	})
	if err != nil {
		return err
	}
	defer repository.Close()
	worker, err := workeradapter.New(config.workerEndpoint, insecure.NewCredentials(), config.maxMessageBytes)
	if err != nil {
		return err
	}
	defer worker.Close()
	executor, err := workflows.NewParseExecutor(repository, worker, workflows.ParseExecutorConfig{
		OwnerID: config.ownerID, AuthScope: config.authScope, Lease: config.lease,
		CallTimeout: config.callTimeout, CancellationPoll: config.cancellationPoll,
		RetryBase: config.retryBase, RetryMax: config.retryMax,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	for ctx.Err() == nil {
		job, response, executeErr := executor.RunOnce(ctx)
		if executeErr == nil {
			if err = encoder.Encode(map[string]any{
				"level": "info", "component": "ingestion-worker", "job_id": job.JobID,
				"attempt": job.Attempt, "fence": job.LeaseFence, "completion": response.Status.String(),
			}); err != nil {
				return fmt.Errorf("write worker event: %w", err)
			}
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if !errors.Is(executeErr, postgres.ErrLeaseUnavailable) {
			if err = encoder.Encode(map[string]any{
				"level": "error", "component": "ingestion-worker", "job_id": job.JobID,
				"attempt": job.Attempt, "fence": job.LeaseFence, "error": executeErr.Error(),
			}); err != nil {
				return fmt.Errorf("write worker error event: %w", err)
			}
		}
		timer := time.NewTimer(config.idlePoll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
	return nil
}

func loadConfig() (runtimeConfig, error) {
	config := runtimeConfig{
		postgresDSN: os.Getenv("REGULAGRAPH_POSTGRES_DSN"), workerEndpoint: os.Getenv("REGULAGRAPH_WORKER_ENDPOINT"),
		ownerID: os.Getenv("REGULAGRAPH_COORDINATOR_OWNER"), authScope: os.Getenv("REGULAGRAPH_COORDINATOR_AUTH_SCOPE"),
	}
	if config.postgresDSN == "" || config.workerEndpoint == "" || config.ownerID == "" || config.authScope == "" {
		return runtimeConfig{}, errors.New("PostgreSQL DSN, worker endpoint, coordinator owner, and auth scope are required")
	}
	if !coordinatorIDPattern.MatchString(config.ownerID) || !printableOpaqueID(config.authScope) {
		return runtimeConfig{}, errors.New("coordinator owner or auth scope is not a valid opaque ID")
	}
	if err := requireLoopback(config.workerEndpoint); err != nil {
		return runtimeConfig{}, err
	}
	var err error
	if config.lease, err = durationEnv("REGULAGRAPH_COORDINATOR_LEASE", 5*time.Minute); err != nil {
		return runtimeConfig{}, err
	}
	if config.callTimeout, err = durationEnv("REGULAGRAPH_COORDINATOR_CALL_TIMEOUT", 4*time.Minute); err != nil {
		return runtimeConfig{}, err
	}
	if config.cancellationPoll, err = durationEnv("REGULAGRAPH_COORDINATOR_CANCELLATION_POLL", 250*time.Millisecond); err != nil {
		return runtimeConfig{}, err
	}
	if config.idlePoll, err = durationEnv("REGULAGRAPH_COORDINATOR_IDLE_POLL", 500*time.Millisecond); err != nil {
		return runtimeConfig{}, err
	}
	if config.retryBase, err = durationEnv("REGULAGRAPH_COORDINATOR_RETRY_BASE", time.Second); err != nil {
		return runtimeConfig{}, err
	}
	if config.retryMax, err = durationEnv("REGULAGRAPH_COORDINATOR_RETRY_MAX", time.Minute); err != nil {
		return runtimeConfig{}, err
	}
	config.maxMessageBytes, err = positiveIntEnv("REGULAGRAPH_WORKER_MAX_MESSAGE_BYTES", workeradapter.DefaultMaxMessageBytes)
	if err != nil {
		return runtimeConfig{}, err
	}
	if config.callTimeout >= config.lease {
		return runtimeConfig{}, errors.New("coordinator call timeout must be shorter than its lease")
	}
	if config.retryMax < config.retryBase {
		return runtimeConfig{}, errors.New("coordinator retry maximum must not be shorter than retry base")
	}
	return config, nil
}

func printableOpaqueID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, character := range []byte(value) {
		if character < 33 || character > 126 {
			return false
		}
	}
	return true
}

func requireLoopback(endpoint string) error {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("worker endpoint must be host:port: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("worker endpoint must be loopback until TLS is configured")
	}
	return nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func positiveIntEnv(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
