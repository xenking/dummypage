package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cache"
	"github.com/gofiber/fiber/v3/middleware/csrf"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/gofiber/template/html/v2"
	"github.com/gofiber/utils/v2"
	"github.com/phuslu/log"

	"github.com/xenking/dummypage/internal/meta"
	logadapter "github.com/xenking/dummypage/pkg/log"
)

var appVersion string

type Server struct {
	*fiber.App
	addr string
	cfg  Config
}

type Config struct {
	Addr    string `default:"localhost:3000"`
	Version string `default:"2.0.0"`

	FilesFolder      string `default:"./files"`
	FilesPrefix      string `default:"files"`
	LargeFilesFolder string `default:"./large"`
	ViewsFolder      string `default:"./static/templates"`
	ViewsExt         string `default:".html"`
	StaticFolder     string `default:"./static"`
	StaticPrefix     string `default:"/"`
	TemplatesPrefix  string `default:"templates"`
}

func New(cfg Config, logger *log.Logger) *Server {
	appVersion = cfg.Version
	s := newServer(cfg)
	return s.setupMiddlewares(cfg, logger).registerRoutes()
}

func newServer(cfg Config) *Server {
	return &Server{
		App: fiber.New(fiber.Config{
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 0, // Disable write timeout
			// Set IdleTimeout high to allow long-running downloads
			IdleTimeout:       60 * time.Minute,
			AppName:           "DummyPage",
			Views:             html.New(cfg.ViewsFolder, cfg.ViewsExt),
			GETOnly:           true,
			StreamRequestBody: false,
			DisableKeepalive:  false,
		}),
		addr: cfg.Addr,
		cfg:  cfg,
	}
}

func (s *Server) setupMiddlewares(cfg Config, logger *log.Logger) *Server {
	s.Use(recover.New())
	s.Use(requestid.New())

	s.Use(csrf.New())
	s.Use(limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusTooManyRequests)
		},
		SkipFailedRequests:     false,
		SkipSuccessfulRequests: true,
		LimiterMiddleware:      limiter.FixedWindow{},
	}))
	s.Use(cache.New(cache.Config{
		Expiration:   10 * time.Minute,
		CacheHeader:  "X-Cache",
		CacheControl: true,
		KeyGenerator: func(c fiber.Ctx) string {
			return utils.CopyString(c.Path())
		},
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/large/")
		},
		Methods: []string{fiber.MethodGet, fiber.MethodHead},
	}))
	s.Use(logadapter.New(logger))

	s.Use(cfg.StaticPrefix, static.New(cfg.StaticFolder, static.Config{
		Compress:      true,
		CacheDuration: 10 * time.Hour,
		MaxAge:        int(time.Hour / time.Second),
	}))
	s.Use(cfg.FilesPrefix, static.New(cfg.FilesFolder, static.Config{
		Compress:      true,
		CacheDuration: 10 * time.Hour,
		MaxAge:        int(time.Hour / time.Second),
	}))

	return s
}

func (s *Server) registerRoutes() *Server {
	s.Get("/", handleIndex())
	s.Get("/version", handleVersion)
	s.Get("/large/:file", s.handleLargeFile())
	s.Use(handleNotFound())

	return s
}

func handleIndex() fiber.Handler {
	return func(ctx fiber.Ctx) error {
		err := ctx.Status(fiber.StatusOK).Render("index", fiber.Map{})
		if err != nil {
			return ctx.Status(500).SendString("Internal Server Error")
		}
		return nil
	}
}

func handleNotFound() fiber.Handler {
	return func(ctx fiber.Ctx) error {
		err := ctx.Status(fiber.StatusNotFound).Render("404", fiber.Map{})
		if err != nil {
			return ctx.Status(500).SendString("Internal Server Error")
		}
		return nil
	}
}
func handleVersion(ctx fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"version":   appVersion,
		"timestamp": time.Now(),
	})
}

func (s *Server) handleLargeFile() fiber.Handler {
	return func(c fiber.Ctx) error {
		reqPath := filepath.Clean(c.Params("file"))
		baseDir := filepath.Clean(s.cfg.LargeFilesFolder)
		full := filepath.Join(baseDir, reqPath)

		if !strings.HasPrefix(full, baseDir+string(os.PathSeparator)) {
			return fiber.ErrForbidden
		}

		if err := c.SendFile(full, fiber.SendFile{Compress: true, Download: true}); err != nil {
			if os.IsNotExist(err) {
				return fiber.ErrNotFound
			}
			return fiber.ErrInternalServerError
		}

		// Attach header only *after* SendFile so it isn't overwritten
		c.Attachment(filepath.Base(full)) // “Content-Disposition: attachment”
		c.Set("X-Accel-Buffering", "no")  // disable proxy buffering (nginx)
		return nil
	}
}

func (s *Server) Run(ctx context.Context) {
	go s.listedShutdown(ctx)

	err := s.Listen(s.addr)
	if err != nil {
		meta.GetLogger(ctx).Error().Err(err).Msg("Listen server")
	}
}

func (s *Server) listedShutdown(ctx context.Context) {
	<-ctx.Done()
	err := s.ShutdownWithTimeout(time.Second * 10)
	if err != nil {
		meta.GetLogger(ctx).Error().Err(err).Msg("Shutdown server")
	}
}
