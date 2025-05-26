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
	s.Get("/large/:file", s.handleLargeFileDownload())
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

// handleLargeFileDownload returns a handler for streaming large files
// This implementation uses Fiber's SendStreamWriter to efficiently stream files
// without loading them entirely into memory, which is essential for large files
func (s *Server) handleLargeFileDownload() fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get the file path from the URL
		path := c.Params("file")
		if path == "" {
			return c.Status(fiber.StatusBadRequest).SendString("Missing file path")
		}

		// For security, sanitize the path and prevent directory traversal
		basePath := s.cfg.LargeFilesFolder
		filePath := filepath.Join(basePath, path)
		if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(basePath)) {
			return c.Status(fiber.StatusForbidden).SendString("Invalid file path")
		}

		// Open the file
		file, err := os.Open(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return c.Status(fiber.StatusNotFound).SendString("File not found")
			}
			return c.Status(fiber.StatusInternalServerError).SendString("Failed to open file")
		}
		// Do NOT use defer file.Close() here!
		// SetBodyStream takes ownership of the file and will close it automatically

		// Get file stats
		stat, err := file.Stat()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("Failed to get file info")
		}

		// Check if file is a directory
		if stat.IsDir() {
			return c.Status(fiber.StatusForbidden).SendString("Cannot download a directory")
		}

		// Set appropriate headers for file download
		c.Set("Content-Type", getContentType(filePath))
		c.Set("Content-Disposition", "attachment; filename=\""+filepath.Base(filePath)+"\"")
		// These settings help with memory usage
		c.Set("Cache-Control", "no-store")
		c.Set("Accept-Ranges", "bytes")

		// X-Accel-Buffering disables proxy buffering for Nginx reverse proxies
		c.Set("X-Accel-Buffering", "no")

		size64 := stat.Size()
		size := int(size64)
		if int64(size) != size64 {
			size = -1
		}
		// Get direct access to the underlying fasthttp context
		c.Response().SetBodyStream(file, size)

		return nil
	}
}

// getContentType determines the content type based on file extension
func getContentType(path string) string {
	ext := filepath.Ext(path)
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".xml":
		return "application/xml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".gz":
		return "application/gzip"
	case ".tar":
		return "application/x-tar"
	default:
		return "application/octet-stream"
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
