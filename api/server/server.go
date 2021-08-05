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
	logger log.Logger
}

type Config struct {
	Addr    string `default:"localhost:3000"`
	Version string `default:"1.0.0"`

	ViewsFolder  string `default:"./views"`
	ViewsExt     string `default:".html"`
	StaticFolder string `default:"./static"`
	StaticPrefix string `default:"/"`
	IndexFile    string `default:"templates/index.html"`

	Limiter limiter.Config
	Cache   cache.Config
	Logger  fiberlogger.Config
}

func New(cfg Config) *Server {
	appVersion = cfg.Version
	s := &Server{
		App: fiber.New(fiber.Config{
			Prefork:      true,
			Views:        html.New(cfg.ViewsFolder, cfg.ViewsExt),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}),
		addr: cfg.Addr,
	}
	return s.setupMiddlewares(cfg)
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
		ByteRange:     false,
		Index:         cfg.IndexFile,
		CacheDuration: 10 * time.Hour,
		MaxAge:        int(time.Hour / time.Second),
	})
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
