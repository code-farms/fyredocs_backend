package handlers

import (
	"github.com/gin-gonic/gin"

	"fyredocs/shared/discord"
)

// AlertReceiver returns the handler that receives Prometheus alert POSTs
// (Alertmanager v2 API) and forwards them to Discord via shared/discord. This
// replaces the standalone Alertmanager container — Prometheus is pointed at this
// endpoint (see deployment/prometheus/prometheus.yml). Built here (not at package
// init) so it reads DISCORD_WEBHOOK_URL after config.LoadConfig has run.
func AlertReceiver() gin.HandlerFunc {
	return discord.AlertWebhookHandler(discord.NewFromEnv())
}
