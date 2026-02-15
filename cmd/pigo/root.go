package pigo

import (
	"fmt"
	"os"

	"github.com/edgeflare/pigo/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string
var logLevel string
var cfg *config.Config
var rootCmd = &cobra.Command{
	Use:   "pigo",
	Short: "PIGO is a PostgreSQL CDC tool",
	Long:  `pigo streams data among endpoints aka peers`,
	Run: func(cmd *cobra.Command, args []string) {
		versionFlag, _ := cmd.Flags().GetBool("version")
		if versionFlag {
			fmt.Println(config.Version)
			return
		}

		// If no subcommand is provided, print help
		cmd.Help()
	},
}

func Main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/pigo.yaml)")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "L", "info", "log requests at this level (debug, info, warn, error, fatal, none)")
	rootCmd.PersistentFlags().BoolP("version", "v", false, "Print the version number")

	// TODO: below flags should be in rest or pipeline cmd
	viper.BindPFlag("postgres.logrepl_conn_string", rootCmd.PersistentFlags().Lookup("postgres.logrepl_conn_string"))
	viper.BindPFlag("postgres.tables", rootCmd.PersistentFlags().Lookup("postgres.tables"))
	rootCmd.PersistentFlags().String("postgres.logrepl_conn_string", "", "PostgreSQL logical replication connection string")
	rootCmd.PersistentFlags().String("postgres.tables", "", "Comma-separated list of tables to replicate")

	// Add the pipeline subcommand
	rootCmd.AddCommand(pipelineCmd)
}

func initConfig() {
	var err error
	cfg, err = config.Load(cfgFile)
	if err != nil {
		fmt.Println("Error loading config:", err)
		os.Exit(1)
	}
}
