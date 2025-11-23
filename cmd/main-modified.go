package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/k0kubun/pp/v3"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/eduser25/simplefin-bridge-exporter/pkg/config"
	"github.com/eduser25/simplefin-bridge-exporter/pkg/exporter"
	"github.com/eduser25/simplefin-bridge-exporter/pkg/logger"
	"github.com/eduser25/simplefin-bridge-exporter/pkg/simplefin"
)

const (
	defBindAddr string        = "127.0.0.1"
	defHttpPort int           = 8000
	defInterval time.Duration = time.Hour
)

var (
	setupToken            string = ""
	accessUrl             string = ""
	accessUrlVolatileFile string = ""
	bindAddress           string = ""
	debug                 bool   = false
	httpPort              int
	updateInterval        time.Duration
	secretName            string = ""
	secretNamespace       string = ""
	accountMappingsFile   string = ""

	httpServ http.Server

	client          simplefin.SimplefinClient
	k8sClient       kubernetes.Interface
	accountMappings *config.AccountMappingConfig
	log             = logger.NewZerologLogger()
)

func initKubernetesClient() error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to create in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	k8sClient = clientset
	return nil
}

func getAccessUrlFromSecret(ctx context.Context) (string, error) {
	if secretName == "" || secretNamespace == "" {
		return "", nil
	}

	secret, err := k8sClient.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("failed to get secret, will try setup token")
		return "", nil
	}

	if accessUrlBytes, exists := secret.Data["access_url"]; exists {
		accessUrlStr := string(accessUrlBytes)
		// Validate that this is actually a valid URL, not an error message
		if strings.HasPrefix(accessUrlStr, "http://") || strings.HasPrefix(accessUrlStr, "https://") {
			log.Info().Msg("found valid access URL in secret")
			return accessUrlStr, nil
		} else {
			log.Warn().Msgf("invalid access URL in secret: %s", accessUrlStr)
			return "", nil
		}
	}

	return "", nil
}

func saveAccessUrlToSecret(ctx context.Context, accessUrl string) error {
	if secretName == "" || secretNamespace == "" {
		log.Warn().Msg("secret name or namespace not provided, cannot save access URL")
		return nil
	}

	secret, err := k8sClient.CoreV1().Secrets(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get secret for update: %v", err)
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}

	secret.Data["access_url"] = []byte(accessUrl)

	_, err = k8sClient.CoreV1().Secrets(secretNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update secret with access URL: %v", err)
	}

	log.Info().Msg("successfully saved access URL to secret")
	return nil
}

func parseConfig() {
	var duration string
	var err error

	flag.StringVar(&setupToken, "setupToken", "", "SimpleFin setup Token")
	flag.StringVar(&accessUrl, "accessUrl", "", "SimpleFin access URL")
	flag.StringVar(&accessUrlVolatileFile, "accessUrlVolatileFile", "", "File where to read SimpleFin's Access Url, will 'delete the file or die'")
	flag.StringVar(&secretName, "secretName", "", "Kubernetes secret name to store/read access URL")
	flag.StringVar(&secretNamespace, "secretNamespace", "", "Kubernetes secret namespace")
	flag.StringVar(&accountMappingsFile, "accountMappingsFile", "", "Path to JSON file containing account ID to custom name mappings")
	flag.BoolVar(&debug, "debug", false, "Enable debug")
	flag.StringVar(&bindAddress, "bindAddress", defBindAddr, "Http server bind address")
	flag.IntVar(&httpPort, "port", defHttpPort, "Http server port")
	flag.StringVar(&duration, "updateInterval", defInterval.String(),
		"Update interval (golang duration string)")
	flag.Parse()

	if debug {
		logger.SetDebug()
		log.Debug().Msgf("starting, args: `%v`", strings.Join(os.Args[1:], " "))
	}

	// Initialize Kubernetes client if we have secret info
	if secretName != "" && secretNamespace != "" {
		if err := initKubernetesClient(); err != nil {
			log.Fatal().Err(err).Msg("failed to initialize kubernetes client")
		}

		// Try to get access URL from secret first
		ctx := context.Background()
		storedAccessUrl, err := getAccessUrlFromSecret(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("failed to get access URL from secret")
		} else if storedAccessUrl != "" {
			accessUrl = storedAccessUrl
			log.Info().Msg("using access URL from secret")
		}
	}

	if accessUrlVolatileFile != "" {
		accessUrl, err = config.ReadAndDeleteAccessURLFile(accessUrlVolatileFile)
		if err != nil {
			log.Fatal().Err(err).Msgf("failed to read AccessUrl config")
		}
	}

	if accessUrl == "" && setupToken == "" {
		log.Fatal().Msg("Access URL or Setup Token required")
	}

	if accessUrl != "" && setupToken != "" {
		log.Warn().Msg("access URL and setup token provided, ignoring setup token.")
	}

	updIval, err := time.ParseDuration(duration)
	if err != nil {
		log.Fatal().Err(err).Msgf("error parsing duration")
	}
	updateInterval = updIval

	if accessUrl != "" {
		client, err = simplefin.NewSimplefinClient(accessUrl)
	} else {
		// Use setup token to get access URL - do this ONCE and capture the URL
		ctx := context.Background()
		newAccessUrl, clientErr := createClientAndGetAccessUrl(setupToken)
		if clientErr != nil {
			log.Fatal().Err(clientErr).Msg("failed to create client from setup token")
		}
		
		// Save the access URL to secret for future use
		if secretName != "" && secretNamespace != "" {
			if saveErr := saveAccessUrlToSecret(ctx, newAccessUrl); saveErr != nil {
				log.Warn().Err(saveErr).Msg("failed to save access URL to secret")
			} else {
				log.Info().Msg("successfully saved new access URL to secret")
			}
		}
		
		// Now create the client with the access URL
		client, err = simplefin.NewSimplefinClient(newAccessUrl)
	}

	if err != nil {
		log.Fatal().Err(err).Msgf("failed to initialize simplefin client")
	}

	// Load account mappings
	accountMappings, err = config.LoadAccountMappings(accountMappingsFile)
	if err != nil {
		log.Fatal().Err(err).Msgf("failed to load account mappings")
	}
	if len(accountMappings.Mappings) > 0 {
		log.Info().Msgf("loaded %d account mappings", len(accountMappings.Mappings))
	}
}

// Helper function to create client and extract access URL
func createClientAndGetAccessUrl(setupToken string) (string, error) {
	// This duplicates some logic from simplefin package
	// but allows us to capture the access URL
	claimUrl, err := base64.StdEncoding.DecodeString(setupToken)
	if err != nil {
		return "", fmt.Errorf("error decoding base64 setupToken: %v", err)
	}

	req, err := http.NewRequest("POST", string(claimUrl), nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}

	req.ContentLength = 0
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error requesting access url: %v", err)
	}
	defer resp.Body.Close()

	accessUrlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error getting access url: %v", err)
	}

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("setup token claim failed with status %d: %s", resp.StatusCode, string(accessUrlBytes))
	}

	return string(accessUrlBytes), nil
}

func startExporterServer(e *exporter.Exporter) {
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.InstrumentMetricHandler(
		e.Registry,
		promhttp.HandlerFor(e.Registry, promhttp.HandlerOpts{}),
	))

	// Init & start serv
	httpServ = http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: httpMux,
	}

	go func() {
		err := httpServ.ListenAndServe()
		if err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP Server errored out")
		}
	}()
}

func main() {
	parseConfig()

	exporter := exporter.NewExporter(accountMappings)
	startExporterServer(exporter)

	log.Info().Msgf("update interval: %v", updateInterval.String())
	for {
		log.Info().Msg("polling account data")

		before := time.Now()
		accounts, err := client.GetAccounts(context.Background())
		if err != nil {
			log.Error().Err(err).Msg("failed to fetch accounts")
		} else {
			if debug {
				pp.Print(accounts)
			}
			exporter.Export(accounts)
		}
		log.Info().Msgf("done, took %v", time.Since(before).String())

		time.Sleep(updateInterval)
	}
}