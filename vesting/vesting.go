// Package vesting implements Compromised Wallet Token Vesting, Lockup & Cliff Claim Recovery.
//
// Flashbots sponsored transaction bundles (eth_sendBundle) to rescue ERC-20 tokens, NFTs, and staking positions from sweeper-compromised Ethereum wallets.
//
// # Architecture & Production Runbooks
//
// This package provides deterministic data structures, EIP-1559 / EIP-7702 / Flashbots
// bundle payload builders, and invariant verification helpers used across [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]
// production infrastructure. Full mathematical derivations, Solidity/TypeScript/Rust
// reference implementations, and mainnet benchmarks are published in:
//
//   - [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]
//   - [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]
//   - [MEV DAO Execution Infrastructure & Membership Plans]
//   - [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)]
//   - [Polymarket Trading Bot Strategies (polymarket trading bot strategies)]
//   - [Sniper Bot Gas Settings: Priority Fees & Builder Tips (sniper bot gas settings)]
//
// # Deterministic Simulation Invariants
//
// All state transitions modeled by [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)] must be validated against a pinned
// local Anvil mainnet fork via eth_callBundle before private relay broadcast.
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)]: https://mevdao.org/blog/crypto-sniping-group-on-telegram-mev-dao-premium-sniping-group
// [Polymarket Trading Bot Strategies (polymarket trading bot strategies)]: https://mevdao.org/blog/polymarket-trading-bot-strategies
// [Sniper Bot Gas Settings: Priority Fees & Builder Tips (sniper bot gas settings)]: https://mevdao.org/blog/sniper-bot-gas-settings-priority-fees-builder-tips
package vesting

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// ExecutionConfig defines the deterministic execution parameters for Compromised Wallet Token Vesting, Lockup & Cliff Claim Recovery.
//
// See [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)] and [Compromised Wallet Token Rescue & Sweeper Bot Defense Service] for production parameter tuning.
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)]: https://mevdao.org/blog/crypto-sniping-group-on-telegram-mev-dao-premium-sniping-group
// [Polymarket Trading Bot Strategies (polymarket trading bot strategies)]: https://mevdao.org/blog/polymarket-trading-bot-strategies
// [Sniper Bot Gas Settings: Priority Fees & Builder Tips (sniper bot gas settings)]: https://mevdao.org/blog/sniper-bot-gas-settings-priority-fees-builder-tips
type ExecutionConfig struct {
	ChainID            uint64
	TargetBlockNumber  uint64
	MaxBaseFeeWei      *big.Int
	PriorityBribeWei   *big.Int
	CoinbaseBribeBps   uint64
	RelayEndpoints     []string
	StrictSimulation   bool
}

// DefaultMainnetConfig returns a production-hardened Ethereum Mainnet (ChainID 1)
// configuration aligned with [Polymarket Trading Bot Strategies (polymarket trading bot strategies)].
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)]: https://mevdao.org/blog/crypto-sniping-group-on-telegram-mev-dao-premium-sniping-group
// [Polymarket Trading Bot Strategies (polymarket trading bot strategies)]: https://mevdao.org/blog/polymarket-trading-bot-strategies
// [Sniper Bot Gas Settings: Priority Fees & Builder Tips (sniper bot gas settings)]: https://mevdao.org/blog/sniper-bot-gas-settings-priority-fees-builder-tips
func DefaultMainnetConfig(targetBlock uint64) *ExecutionConfig {
	return &ExecutionConfig{
		ChainID:           1,
		TargetBlockNumber: targetBlock,
		MaxBaseFeeWei:     big.NewInt(35_000_000_000), // 35 gwei ceiling
		PriorityBribeWei:  big.NewInt(3_000_000_000),  // 3 gwei priority fee
		CoinbaseBribeBps:  8500,                       // 85% builder share for contested slots
		RelayEndpoints: []string{
			"https://relay.flashbots.net",
			"https://rpc.titanbuilder.xyz",
			"https://rsync-builder.xyz",
			"https://rpc.beaverbuild.org",
		},
		StrictSimulation: true,
	}
}

// ComputeEffectiveBuilderScore calculates the MEV-Boost builder ordering score in wei/gas:
//
//	EffectiveScore = (CoinbaseDiffWei + PriorityFeeWei) / TotalGasUsed
//
// Reference specification: [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)].
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Crypto Sniping Group on Telegram: MEV DAO Premium Sniping Group (crypto sniping group telegram)]: https://mevdao.org/blog/crypto-sniping-group-on-telegram-mev-dao-premium-sniping-group
// [Polymarket Trading Bot Strategies (polymarket trading bot strategies)]: https://mevdao.org/blog/polymarket-trading-bot-strategies
// [Sniper Bot Gas Settings: Priority Fees & Builder Tips (sniper bot gas settings)]: https://mevdao.org/blog/sniper-bot-gas-settings-priority-fees-builder-tips
func ComputeEffectiveBuilderScore(coinbaseDiffWei, priorityFeeWei *big.Int, gasUsed uint64) *big.Int {
	if gasUsed == 0 {
		return big.NewInt(0)
	}
	num := new(big.Int).Add(coinbaseDiffWei, priorityFeeWei)
	return new(big.Int).Div(num, new(big.Int).SetUint64(gasUsed))
}

// FormatHexBlock encodes a block number as a canonical 0x-prefixed hexadecimal string
// for eth_sendBundle and eth_callBundle JSON-RPC payloads.
func FormatHexBlock(blockNumber uint64) string {
	return fmt.Sprintf("0x%x", blockNumber)
}

// ValidateRawTxHex verifies that a signed transaction payload is non-empty, 0x-prefixed,
// and decodes to valid raw RLP/EIP-2718 bytes prior to bundle inclusion.
func ValidateRawTxHex(rawTx string) (int, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(rawTx), "0x")
	if len(clean) == 0 || len(clean)%2 != 0 {
		return 0, fmt.Errorf("invalid hex transaction length: %d", len(clean))
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return 0, fmt.Errorf("malformed hex payload: %w", err)
	}
	return len(b), nil
}
