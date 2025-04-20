package server

import (
	"context"
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
}

type Config struct {
	Addr    string `default:"localhost:3000"`
	Version string `default:"2.0.0"`

	FilesFolder     string `default:"./files"`
	FilesPrefix     string `default:"files"`
	ViewsFolder     string `default:"./static/templates"`
	ViewsExt        string `default:".html"`
	StaticFolder    string `default:"./static"`
	StaticPrefix    string `default:"/"`
	TemplatesPrefix string `default:"templates"`
}

func New(cfg Config, logger *log.Logger) *Server {
	appVersion = cfg.Version
	s := newServer(cfg, logger)
	return s.setupMiddlewares(cfg, logger).registerRoutes()
}

func newServer(cfg Config, logger *log.Logger) *Server {
	return &Server{
		App: fiber.New(fiber.Config{
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			AppName:           "DummyPage",
			Views:             html.New(cfg.ViewsFolder, cfg.ViewsExt),
			GETOnly:           true,
			StreamRequestBody: false,
		}),
		addr: cfg.Addr,
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
