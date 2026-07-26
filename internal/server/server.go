package server

import (
	"context"
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

	FilesFolder             string `default:"./files"`
	FilesPrefix             string `default:"files"`
	LargeFilesFolder        string `default:"./large"`
	LargeFilesPrefix        string `default:"large"`
	CoursesCatalog          string `default:"./data/catalog.json.gz"`
	CoursesPasswordHash     string
	CoursesPasswordHashFile string
	ViewsFolder             string `default:"./static/templates"`
	ViewsExt                string `default:".html"`
	StaticFolder            string `default:"./static"`
	StaticPrefix            string `default:"/"`
	TemplatesPrefix         string `default:"templates"`
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
			GETOnly:           false,
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
	s.Use("/courses", coursesSecurityHeaders)

	s.Use(csrf.New(csrf.Config{
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/courses/api/")
		},
	}))
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
		Expiration:  10 * time.Minute,
		CacheHeader: "X-Cache",
		Next: func(c fiber.Ctx) bool {
			skip := strings.HasPrefix(c.Path(), "/large/") ||
				strings.HasPrefix(c.Path(), "/courses/api/")
			return skip
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
	s.Use(cfg.LargeFilesPrefix, static.New(cfg.LargeFilesFolder, static.Config{
		Compress:      false,
		Download:      true,
		ByteRange:     true,
		CacheDuration: -1,
	}))

	return s
}

func (s *Server) registerRoutes() *Server {
	s.Get("/", handleIndex())
	s.Get("/courses", handleCourses())
	s.Get("/courses/api/meta", limiter.New(limiter.Config{
		Max:               60,
		Expiration:        1 * time.Minute,
		LimiterMiddleware: limiter.FixedWindow{},
	}), handleCoursesMeta(s.cfg))
	s.Post("/courses/api/catalog", limiter.New(limiter.Config{
		Max:                    5,
		Expiration:             15 * time.Minute,
		SkipSuccessfulRequests: true,
		LimiterMiddleware:      limiter.FixedWindow{},
	}), handleCoursesCatalog(s.cfg))
	s.Get("/version", handleVersion)
	s.Use(handleNotFound())

	return s
}

func handleCourses() fiber.Handler {
	return func(ctx fiber.Ctx) error {
		if err := ctx.Status(fiber.StatusOK).Render("courses", fiber.Map{}); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).SendString("Internal Server Error")
		}
		return nil
	}
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
