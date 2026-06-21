// Command acgd — точка входа ACG.
//
// Два режима (выбор источника/хранилища — это инверсия зависимостей в действии):
//
//	acgd --serve                 запустить HTTP API (collect/analyze/edges по REST)
//	acgd                          one-shot: собрать снапшот, посчитать cluster-граф, напечатать JSON
//
// Источник и хранилище переключаются флагами/окружением без изменения доменного кода:
//
//	--source cluster              брать манифесты из кластера (client-go)
//	--source dir:./test/golden    брать манифесты из директории YAML (демо/CI без кластера)
//	--store  memory|postgres
//	--blob   memory|minio
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yourname/acg/internal/api"
	"github.com/yourname/acg/internal/collect"
	"github.com/yourname/acg/internal/collect/fsdir"
	"github.com/yourname/acg/internal/collect/kube"
	"github.com/yourname/acg/internal/config"
	"github.com/yourname/acg/internal/service"
	"github.com/yourname/acg/internal/store"
	"github.com/yourname/acg/internal/store/memory"
	"github.com/yourname/acg/internal/store/miniostore"
	"github.com/yourname/acg/internal/store/postgres"
)

func main() {
	var (
		serve      = flag.Bool("serve", false, "запустить HTTP-сервер вместо one-shot анализа")
		source     = flag.String("source", "", "источник: cluster | dir:<path> (по умолчанию из ACG_SOURCE)")
		storeKind  = flag.String("store", "", "хранилище БД: memory | postgres")
		blobKind   = flag.String("blob", "", "blob: memory | minio")
		scope      = flag.String("scope", "cluster", "область анализа для one-shot: cluster | namespace:<ns> | workload:<ns>/<name>")
		kubeconfig = flag.String("kubeconfig", "", "путь к kubeconfig (если пусто — in-cluster)")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("конфигурация: %v", err)
	}
	// CLI-флаги переопределяют окружение.
	if *source != "" {
		cfg.DefaultSource = *source
	}
	if *storeKind != "" {
		cfg.StoreKind = *storeKind
	}
	if *blobKind != "" {
		cfg.BlobKind = *blobKind
	}

	ctx := context.Background()

	db, blob, closer, err := buildStores(ctx, cfg)
	if err != nil {
		log.Fatalf("инициализация хранилищ: %v", err)
	}
	defer closer()

	newReader := readerFactory(cfg.DefaultSource, *kubeconfig)
	svc := service.New(db, blob)

	if *serve {
		runServer(ctx, cfg, svc, db, newReader)
		return
	}
	runOneShot(ctx, svc, db, newReader, *scope)
}

// buildStores собирает выбранные адаптеры DB/Blob и возвращает функцию закрытия ресурсов.
func buildStores(ctx context.Context, cfg config.Config) (store.DB, store.Blob, func(), error) {
	// memory-стор реализует и DB, и Blob — если оба memory, используем один объект.
	if cfg.StoreKind == "memory" && cfg.BlobKind == "memory" {
		m := memory.New()
		return m, m, func() {}, nil
	}

	var (
		db    store.DB
		blob  store.Blob
		close = func() {}
	)

	switch cfg.StoreKind {
	case "memory":
		db = memory.New()
	case "postgres":
		pg, err := postgres.New(ctx, cfg.PostgresDSN)
		if err != nil {
			return nil, nil, nil, err
		}
		db = pg
		close = pg.Close
	default:
		return nil, nil, nil, fmt.Errorf("неизвестный store %q", cfg.StoreKind)
	}

	switch cfg.BlobKind {
	case "memory":
		blob = memory.New()
	case "minio":
		mb, err := miniostore.New(ctx, miniostore.Options{
			Endpoint:  cfg.MinioEndpoint,
			AccessKey: cfg.MinioAccessKey,
			SecretKey: cfg.MinioSecretKey,
			Bucket:    cfg.MinioBucket,
			UseSSL:    cfg.MinioUseSSL,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		blob = mb
	default:
		return nil, nil, nil, fmt.Errorf("неизвестный blob %q", cfg.BlobKind)
	}

	return db, blob, close, nil
}

// readerFactory возвращает фабрику источника по строке конфигурации.
func readerFactory(sourceSpec, kubeconfig string) api.ReaderFactory {
	return func() (collect.Reader, error) {
		switch {
		case sourceSpec == "cluster":
			if kubeconfig != "" {
				return kube.NewFromKubeconfig(kubeconfig)
			}
			return kube.NewInCluster()
		case strings.HasPrefix(sourceSpec, "dir:"):
			dir := strings.TrimPrefix(sourceSpec, "dir:")
			return fsdir.New(dir), nil
		default:
			return nil, fmt.Errorf("неизвестный источник %q (ожидался cluster | dir:<path>)", sourceSpec)
		}
	}
}

func runServer(ctx context.Context, cfg config.Config, svc *service.Service, db store.DB, newReader api.ReaderFactory) {
	srv := api.NewServer(svc, db, newReader)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("ACG слушает %s (source=%s store=%s blob=%s)", cfg.HTTPAddr, cfg.DefaultSource, cfg.StoreKind, cfg.BlobKind)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("HTTP: %v", err)
	}
	_ = ctx
}

// runOneShot: собрать → проанализировать → напечатать рёбра JSON. Удобно для CI и демонстрации.
func runOneShot(ctx context.Context, svc *service.Service, db store.DB, newReader api.ReaderFactory, scope string) {
	reader, err := newReader()
	if err != nil {
		log.Fatalf("источник: %v", err)
	}
	snapID, err := svc.Collect(ctx, "oneshot", "CLUSTER", reader)
	if err != nil {
		log.Fatalf("сбор: %v", err)
	}
	runID, err := svc.Analyze(ctx, snapID, scope)
	if err != nil {
		log.Fatalf("анализ: %v", err)
	}
	edges, err := db.GetEdges(ctx, runID)
	if err != nil {
		log.Fatalf("чтение рёбер: %v", err)
	}
	run, _ := db.GetRun(ctx, runID)

	fmt.Fprintf(os.Stderr, "snapshot=%d run=%d scope=%s status=%s edges=%d\n",
		snapID, runID, scope, run.Status, len(edges))

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(edges); err != nil {
		log.Fatalf("вывод: %v", err)
	}
}
