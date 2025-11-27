package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DevN0mad/OpenProjectBot/internal/services"
)

const APIv1Prefix = "/api/v1/"

// AdminServerOpts параметры для настройки административного сервера.
type AdminServerOpts struct {
	Address             string `yaml:"address" validate:"required"`
	ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds" validate:"min=0"`
	WriteTimeoutSeconds int    `yaml:"write_timeout_seconds" validate:"min=0"`
	IdleTimeoutSeconds  int    `yaml:"idle_timeout_seconds" validate:"min=0"`
}

// AdminHandler обрабатывает административные команды.
type AdminServer struct {
	logger *slog.Logger
	opts   *AdminServerOpts
	srv    *http.Server
	opSrv  *services.OpenProjectService
}

// NewAdminHandler создаёт новый обработчик для административных команд.
func NewAdminHandler(logger *slog.Logger, opSrv *services.OpenProjectService, opts *AdminServerOpts) *AdminServer {
	return &AdminServer{
		logger: logger,
		opts:   opts,
		opSrv:  opSrv,
	}
}

// Register регистрирует маршруты административного сервера.
func (h *AdminServer) Register(mux *http.ServeMux) {
	mux.HandleFunc(withPrefix("report"), h.handleReport)

}

// handleReport обрабатывает запросы на получение отчёта.
func (h *AdminServer) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.logger.Warn("Method not allowed", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	report, err := h.opSrv.GenerateExcelReport()
	if err != nil {
		h.logger.Error("Generate report", "error", err)
		http.Error(w, "Failed to generate report", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(report))

}

// Start запускает административный сервер.
func (h *AdminServer) Start(ctx context.Context) error {
	h.logger.Info("Starting admin server", "address", h.opts.Address)
	mux := http.NewServeMux()
	h.Register(mux)
	h.srv = &http.Server{
		Addr:         h.opts.Address,
		ReadTimeout:  time.Duration(h.opts.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(h.opts.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(h.opts.IdleTimeoutSeconds) * time.Second,
		Handler:      mux,
	}

	go func() {
		<-ctx.Done()

		h.logger.Info("Shutting down admin server (ctx canceled)")

		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := h.srv.Shutdown(shCtx); err != nil && err != http.ErrServerClosed {
			h.logger.Error("Admin server shutdown error", "error", err)
		}
	}()

	if err := h.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		h.logger.Error("Admin server error", "error", err)
		return err
	}

	h.logger.Info("Admin server stopped")
	return nil
}

// withPrefix добавляет префикс к пути API.
func withPrefix(postfix string) string {
	return APIv1Prefix + strings.TrimSpace(postfix)
}
