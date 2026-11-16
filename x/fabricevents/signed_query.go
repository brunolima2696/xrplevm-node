package fabricevents

import (
	"encoding/json"
	"fmt"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/spf13/cobra"
)

// QuerySignedTransaction reads committed consensus state. Unlike an Ethereum
// indexer lookup, it also represents a rejection recorded in an IBC transaction.
func QuerySignedTransaction(ctx client.Context, hash string) (*SignedTransactionRecord, error) {
	normalized, err := normalizeSignedHash(hash)
	if err != nil {
		return nil, err
	}
	return querySignedRecord(ctx, signedHashKey(normalized))
}
func querySignedRecord(ctx client.Context, key []byte) (*SignedTransactionRecord, error) {
	bz, _, err := ctx.QueryWithData("store/"+ModuleName+"/key", key)
	if err != nil {
		return nil, err
	}
	if len(bz) == 0 {
		return nil, nil
	}
	var result SignedTransactionRecord
	if err := json.Unmarshal(bz, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func newSignedTransactionQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "transaction HASH", Short: "Query an IBC-carried EVM transaction/result by hash", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := client.GetClientQueryContext(cmd)
		if err != nil {
			return err
		}
		record, err := QuerySignedTransaction(ctx, args[0])
		if err != nil {
			return err
		}
		return printSignedRecord(cmd, record)
	}}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
func newSignedOrderQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "payment SOURCE_CHAIN ID_PEDIDO", Short: "Query an executed signed payment by source and order ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if !validTransactionSource(args[0]) || !validOrderID(args[1]) {
			return fmt.Errorf("invalid source chain or id_pedido")
		}
		ctx, err := client.GetClientQueryContext(cmd)
		if err != nil {
			return err
		}
		record, err := querySignedRecord(ctx, signedOrderKey(args[0], args[1]))
		if err != nil {
			return err
		}
		return printSignedRecord(cmd, record)
	}}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}
func printSignedRecord(cmd *cobra.Command, record *SignedTransactionRecord) error {
	if record == nil {
		return fmt.Errorf("signed transaction/payment not found")
	}
	bz, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(bz))
	return err
}
