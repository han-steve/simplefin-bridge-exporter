package exporter

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/eduser25/simplefin-bridge-exporter/pkg/config"
	"github.com/eduser25/simplefin-bridge-exporter/pkg/logger"
	"github.com/eduser25/simplefin-bridge-exporter/pkg/simplefin"
)

const (
	namespace = "simplefin"
)

var (
	log = logger.NewZerologLogger()
)

type Exporter struct {
	Registry          *prometheus.Registry
	balances          *prometheus.GaugeVec
	availableBalances *prometheus.GaugeVec
	last_updated      *prometheus.GaugeVec
	AccountMappings   *config.AccountMappingConfig
}

func NewExporter(accountMappings *config.AccountMappingConfig) *Exporter {
	exporter := &Exporter{
		Registry: prometheus.NewRegistry(),
		AccountMappings: accountMappings,
		balances: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "balance",
				Help:      "Account balance",
			},
			[]string{"domain", "account_name", "account_id", "currency"},
		),
		availableBalances: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "available_balance",
				Help:      "Available account balance",
			},
			[]string{"domain", "account_name", "account_id", "currency"},
		),
		last_updated: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "last_updated",
				Help:      "Last updated, in Epoch Unitx Timestamp as reported by simplefin",
			},
			[]string{"domain", "account_name", "account_id"},
		),
	}
	exporter.Registry.MustRegister(exporter.balances)
	exporter.Registry.MustRegister(exporter.availableBalances)
	exporter.Registry.MustRegister(exporter.last_updated)

	return exporter
}

func (e *Exporter) Export(accounts *simplefin.Accounts) error {
	for _, accItem := range accounts.Accounts {
		// Skip ignored accounts
		if e.AccountMappings.IsAccountIgnored(accItem.ID) {
			log.Debug().Msgf("Skipping ignored account: %s (ID: %s)", accItem.Name, accItem.ID)
			continue
		}
		
		// Get the account name (either mapped or original)
		accountName := e.AccountMappings.GetAccountNameMapping(accItem.ID, accItem.Name)
		
		bal, err := strconv.ParseFloat(accItem.Balance, 32)
		if err != nil {
			log.Error().Err(err).Msgf("Could not parse balance from %v - %v (ID: %v)",
				accItem.Org.Domain, accItem.Name, accItem.ID)

		} else {
			e.balances.WithLabelValues(accItem.Org.Domain, accountName, accItem.ID, accItem.Currency).Set(bal)
		}

		availBal, err := strconv.ParseFloat(accItem.AvailableBalance, 32)
		if err != nil {
			log.Error().Err(err).Msgf("Could not parse available balance from %v - %v (ID: %v)",
				accItem.Org.Domain, accItem.Name, accItem.ID)

		} else {
			e.availableBalances.WithLabelValues(accItem.Org.Domain, accountName, accItem.ID, accItem.Currency).Set(availBal)
		}

		e.last_updated.WithLabelValues(accItem.Org.Domain, accountName, accItem.ID).Set(float64(accItem.BalanceDate))

	}
	return nil
}
