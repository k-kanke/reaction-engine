// Package postsessiontrigger starts the post-session report pipeline
// (r-post-session-job then r-pdf-renderer) for a finished session, via the
// Cloud Run Admin API's jobs.run method. plan/post-session-report-
// implementation.md Step 5 decision 2: gateway calls the Admin API
// directly on session_end -- no Pub/Sub or Eventarc in between.
package postsessiontrigger

import (
	"context"
	"fmt"
	"log"

	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
)

// Trigger starts the pipeline for one session_id. Implementations must not
// block the caller for longer than they're willing to hold up whatever
// triggered it (gateway.Handler.handleSessionEnd runs this in its own
// goroutine with a background context precisely so a slow pipeline can't
// stall the WebSocket read loop or outlive the connection).
type Trigger interface {
	TriggerSessionEnd(ctx context.Context, sessionID string)
}

// CloudRunTrigger runs post-session-job then pdf-renderer as Cloud Run Job
// executions, waiting for each to finish before starting the next --
// pdf-renderer reads back the `reports` row post-session-job just wrote, so
// it would find nothing (or a stale report) if the two ran concurrently.
// gmail-sender isn't chained here: it also requires a recipient email,
// which Step 8 wires up from the extension. Until then it's triggered
// manually (`gcloud run jobs execute r-gmail-sender --args=...`).
type CloudRunTrigger struct {
	client             *run.JobsClient
	postSessionJobName string
	pdfRendererJobName string
}

// NewCloudRunTrigger dials the Cloud Run Admin API using Application
// Default Credentials -- on Cloud Run this is gateway's own runtime
// identity (module.gateway_service_account), which Step 4's Terraform
// change already granted roles/run.invoker on both jobs.
func NewCloudRunTrigger(ctx context.Context, projectID, region string) (*CloudRunTrigger, error) {
	client, err := run.NewJobsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("postsessiontrigger: new jobs client: %w", err)
	}
	return &CloudRunTrigger{
		client:             client,
		postSessionJobName: fmt.Sprintf("projects/%s/locations/%s/jobs/r-post-session-job", projectID, region),
		pdfRendererJobName: fmt.Sprintf("projects/%s/locations/%s/jobs/r-pdf-renderer", projectID, region),
	}, nil
}

func (t *CloudRunTrigger) TriggerSessionEnd(ctx context.Context, sessionID string) {
	if err := t.runJob(ctx, t.postSessionJobName, "--session-id="+sessionID); err != nil {
		log.Printf("postsessiontrigger: post-session-job failed for session_id=%s: %v", sessionID, err)
		return
	}
	if err := t.runJob(ctx, t.pdfRendererJobName, "--session-id="+sessionID); err != nil {
		log.Printf("postsessiontrigger: pdf-renderer failed for session_id=%s: %v", sessionID, err)
		return
	}
	log.Printf("postsessiontrigger: post-session pipeline complete for session_id=%s", sessionID)
}

// runJob starts one execution with args overriding the job's default
// (empty) args, then blocks until that execution finishes. Overrides.
// ContainerOverrides[0].Name is left blank: per the Cloud Run Admin API,
// that's valid (and required for us, since we don't track container names
// here) when the job has exactly one container, which is true for all
// three jobs modules/cloud-run-job creates.
func (t *CloudRunTrigger) runJob(ctx context.Context, jobName string, args ...string) error {
	op, err := t.client.RunJob(ctx, &runpb.RunJobRequest{
		Name: jobName,
		Overrides: &runpb.RunJobRequest_Overrides{
			ContainerOverrides: []*runpb.RunJobRequest_Overrides_ContainerOverride{
				{Args: args},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("run job %s: %w", jobName, err)
	}

	execution, err := op.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait for %s execution: %w", jobName, err)
	}
	if execution.GetFailedCount() > 0 {
		return fmt.Errorf("%s execution %s had %d failed task(s)", jobName, execution.GetName(), execution.GetFailedCount())
	}
	return nil
}
