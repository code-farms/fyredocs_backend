package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go/jetstream"

	"fyredocs/shared/logger"
	"fyredocs/shared/natsconn"
	"fyredocs/shared/queue"
	"fyredocs/shared/response"
)

// redrivableServices are the worker services whose DLQ messages are original job
// payloads and can be re-dispatched. Event-consumer DLQs (notification/analytics/
// document) hold JobEvents, not dispatchable payloads, so they are left in place.
var redrivableServices = map[string]bool{
	"convert-to-pdf":   true,
	"convert-from-pdf": true,
	"organize-pdf":     true,
	"optimize-pdf":     true,
}

// RedriveDLQ re-dispatches dead-lettered worker jobs from JOBS_DLQ back onto their
// service's dispatch subject, then removes them from the DLQ (idempotent — a
// redriven message won't be driven twice). Super-admin only.
//
//	POST /api/jobs/dlq/redrive?limit=50
func RedriveDLQ(c *gin.Context) {
	if strings.TrimSpace(c.GetHeader("X-User-Role")) != "super-admin" {
		response.Forbidden(c, response.CodeForbidden, "Super-admin access is required.")
		return
	}
	if natsconn.JS == nil {
		response.Errorf(c, http.StatusServiceUnavailable, response.CodeServiceUnavailable,
			"The job queue is temporarily unavailable.", errors.New("nats jetstream not connected"), "op", "dlq.redrive")
		return
	}

	limit := 50
	if v, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil && v > 0 && v <= 500 {
		limit = v
	}

	ctx := c.Request.Context()
	stream, err := natsconn.JS.Stream(ctx, "JOBS_DLQ")
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not open the dead-letter queue.", err, "op", "dlq.stream")
		return
	}
	cons, err := natsconn.JS.CreateConsumer(ctx, "JOBS_DLQ", jetstream.ConsumerConfig{
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		FilterSubject: "jobs.dlq.>",
	})
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not open the dead-letter queue.", err, "op", "dlq.consumer_create")
		return
	}
	defer func() {
		_ = natsconn.JS.DeleteConsumer(ctx, "JOBS_DLQ", cons.CachedInfo().Name)
	}()

	batch, err := cons.Fetch(limit)
	if err != nil {
		response.InternalErrorf(c, response.CodeServerError, "Could not read the dead-letter queue.", err, "op", "dlq.fetch")
		return
	}

	var redriven, skipped, failed int
	for msg := range batch.Messages() {
		service := strings.TrimPrefix(msg.Subject(), "jobs.dlq.")
		if !redrivableServices[service] {
			_ = msg.Nak() // leave non-worker DLQ entries in place for their owners
			skipped++
			continue
		}
		if _, perr := natsconn.JS.Publish(ctx, queue.SubjectForDispatch(service), msg.Data()); perr != nil {
			logger.LogWarn(ctx, "dlq.republish", perr, "service", service)
			_ = msg.Nak()
			failed++
			continue
		}
		// Remove from the DLQ so a re-run doesn't re-dispatch the same job.
		if meta, merr := msg.Metadata(); merr == nil {
			if derr := stream.DeleteMsg(ctx, meta.Sequence.Stream); derr != nil {
				logger.LogWarn(ctx, "dlq.delete_msg", derr, "service", service, "seq", meta.Sequence.Stream)
			}
		}
		_ = msg.Ack()
		redriven++
	}

	response.OK(c, "Dead-letter redrive complete", gin.H{
		"redriven": redriven, "skipped": skipped, "failed": failed,
	})
}
