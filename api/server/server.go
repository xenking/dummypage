package server

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/gofiber/template/html"
	"github.com/phuslu/log"
	fiberlogger "github.com/phuslu/log-contrib/fiber"
)

var appVersion string

type Server struct {
	*fiber.App
	addr   string
	logger *log.Logger
}

type Config struct {
	Addr    string `default:"localhost:3000"`
	Version string `default:"1.0.0"`

	Prefork         bool
	FilesFolder     string `default:"./files"`
	FilesPrefix     string `default:"files"`
	ViewsFolder     string `default:"./static/templates"`
	ViewsExt        string `default:".html"`
	StaticFolder    string `default:"./static"`
	StaticPrefix    string `default:"/"`
	TemplatesPrefix string `default:"templates"`

	Limiter limiter.Config
	Cache   cache.Config
	Logger  fiberlogger.Config
}

func New(cfg Config) *Server {
	appVersion = cfg.Version
	s := &Server{
		App: fiber.New(fiber.Config{
			Prefork:           cfg.Prefork,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			AppName:           "DummyPage",
			Views:             html.New(cfg.ViewsFolder, cfg.ViewsExt),
			GETOnly:           true,
			StreamRequestBody: true,
		}),
		addr:   cfg.Addr,
		logger: cfg.Logger.Logger,
	}
	return s.setupMiddlewares(cfg)
}

func notFoundHandler(cfg Config) fiber.Handler {
	return func(ctx *fiber.Ctx) error {

		err := ctx.Status(fiber.StatusNotFound).Render("404", fiber.Map{})
		if err != nil {
			return ctx.Status(500).SendString("Internal Server Error")
		}
		return nil
	}
}

func (s *Server) setupMiddlewares(cfg Config) *Server {
	s.App.Use(recover.New())
	s.App.Use(requestid.New())

	s.App.Use(csrf.New())
	s.App.Use(limiter.New(cfg.Limiter))
	s.App.Use(cache.New(cfg.Cache))
	s.App.Use(fiberlogger.SetLogger(cfg.Logger))

	s.App.Static(cfg.StaticPrefix, cfg.StaticFolder, fiber.Static{
		Compress:      true,
		Index:         cfg.TemplatesPrefix + "/index.html",
		CacheDuration: 10 * time.Hour,
		MaxAge:        int(time.Hour / time.Second),
	})
	s.App.Static(cfg.FilesPrefix, cfg.FilesFolder, fiber.Static{
		Compress:      true,
		CacheDuration: 10 * time.Hour,
		MaxAge:        int(time.Hour / time.Second),
	})
	s.App.Use(notFoundHandler(cfg))

	return s
}

func (s *Server) registerRoutes() *Server {
	s.Get("/version", handleVersion)
	return s
}

func handleVersion(ctx *fiber.Ctx) error {
	return ctx.JSON(fiber.Map{
		"version":   appVersion,
		"timestamp": time.Now(),
	})
}

func (s *Server) Run(ctx context.Context) {
	err := s.Listen(s.addr)
	s.logger.Fatal().Err(err).Msg("Listen server")
}
