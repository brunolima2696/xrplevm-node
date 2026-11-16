package fabricevents

import (
	"encoding/json"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/spf13/cobra"
)

func newTxCmd() *cobra.Command {
	root := &cobra.Command{Use: "fabric-events", Short: "Manage Fabric IBC execution"}
	cmd := &cobra.Command{
		Use: "update-config", Short: "Submit a governance proposal to replace the bridge configuration",
		Long: "Submit a signed governance proposal. Configuration changes only after the proposal passes and executes. Use --generate-message to print the nested governance message without broadcasting.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clients, _ := cmd.Flags().GetStringSlice("allowed-client")
			gas, _ := cmd.Flags().GetUint64("execution-gas-limit")
			msg := &MsgUpdateConfig{Authority: ConfigAuthority(), AllowedClientIds: clients, GasLimit: gas}
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			generate, _ := cmd.Flags().GetBool("generate-message")
			if generate {
				// A plain proposal message, not a signed transaction or a state update.
				document := map[string]interface{}{"@type": sdk.MsgTypeURL(msg), "authority": msg.Authority,
					"allowed_client_ids": clients, "gas_limit": fmt.Sprint(gas)}
				bz, err := json.MarshalIndent(document, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(bz))
				return err
			}
			deposit, _ := cmd.Flags().GetString("deposit")
			coins, err := sdk.ParseCoinsNormalized(deposit)
			if err != nil || coins.Empty() {
				return fmt.Errorf("provide --deposit with the governance proposal deposit, e.g. 1axrp")
			}
			ctx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			packed, err := codectypes.NewAnyWithValue(msg)
			if err != nil {
				return err
			}
			title, _ := cmd.Flags().GetString("title")
			summary, _ := cmd.Flags().GetString("summary")
			proposal := &govv1.MsgSubmitProposal{Messages: []*codectypes.Any{packed}, InitialDeposit: coins,
				Proposer: ctx.GetFromAddress().String(), Title: title, Summary: summary}
			return tx.GenerateOrBroadcastTxCLI(ctx, cmd.Flags(), proposal)
		},
	}
	cmd.Flags().StringSlice("allowed-client", nil, "authorized Fabric MSP client IDs (repeat or comma-separate)")
	cmd.Flags().Uint64("execution-gas-limit", DefaultConfig().GasLimit, "gas cap for internal EVM execution")
	cmd.Flags().Bool("generate-message", false, "print the unsigned governance message without submitting")
	cmd.Flags().String("deposit", "", "initial governance proposal deposit")
	cmd.Flags().String("title", "Configure Fabric IBC execution", "governance proposal title")
	cmd.Flags().String("summary", "Update authorized Fabric clients and EVM execution configuration", "governance proposal summary")
	flags.AddTxFlagsToCmd(cmd)
	root.AddCommand(cmd)
	return root
}

func newConfigQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Query the on-chain bridge configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			bz, _, err := ctx.QueryWithData("store/"+ModuleName+"/key", []byte("config"))
			if err != nil {
				return err
			}
			if len(bz) == 0 {
				return fmt.Errorf("fabric-events configuration not found")
			}
			var config Config
			if err := json.Unmarshal(bz, &config); err != nil {
				return err
			}
			out, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return err
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
