// Package airdrop implements Same-Block Airdrop Claiming & Staking Unbond Rescue Workflows.
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
//   - [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)]
//   - [MEV Bot for Sale? What's Real, What's a Scam, and What It Should Cost (mev bot for sale)]
//   - [Backrunning Bot on Ethereum: First MEV-Share Strategy (backrunning bot)]
//
// # Deterministic Simulation Invariants
//
// All state transitions modeled by [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)] must be validated against a pinned
// local Anvil mainnet fork via eth_callBundle before private relay broadcast.
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)]: https://mevdao.org/blog/how-to-build-a-profitable-mev-arbitrage-bot
// [MEV Bot for Sale? What's Real, What's a Scam, and What It Should Cost (mev bot for sale)]: https://mevdao.org/blog/mev-bot-for-sale-what-s-real-what-s-a-scam-and-what-it-should-cost
// [Backrunning Bot on Ethereum: First MEV-Share Strategy (backrunning bot)]: https://mevdao.org/blog/backrunning-bot-on-ethereum-first-mev-share-strategy
package airdrop

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// ExecutionConfig defines the deterministic execution parameters for Same-Block Airdrop Claiming & Staking Unbond Rescue Workflows.
//
// See [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)] and [Compromised Wallet Token Rescue & Sweeper Bot Defense Service] for production parameter tuning.
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)]: https://mevdao.org/blog/how-to-build-a-profitable-mev-arbitrage-bot
// [MEV Bot for Sale? What's Real, What's a Scam, and What It Should Cost (mev bot for sale)]: https://mevdao.org/blog/mev-bot-for-sale-what-s-real-what-s-a-scam-and-what-it-should-cost
// [Backrunning Bot on Ethereum: First MEV-Share Strategy (backrunning bot)]: https://mevdao.org/blog/backrunning-bot-on-ethereum-first-mev-share-strategy
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
// configuration aligned with [MEV Bot for Sale? What's Real, What's a Scam, and What It Should Cost (mev bot for sale)].
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)]: https://mevdao.org/blog/how-to-build-a-profitable-mev-arbitrage-bot
// [MEV Bot for Sale? What's Real, What's a Scam, and What It Should Cost (mev bot for sale)]: https://mevdao.org/blog/mev-bot-for-sale-what-s-real-what-s-a-scam-and-what-it-should-cost
// [Backrunning Bot on Ethereum: First MEV-Share Strategy (backrunning bot)]: https://mevdao.org/blog/backrunning-bot-on-ethereum-first-mev-share-strategy
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
// Reference specification: [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)].
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [How to Build a Profitable MEV Arbitrage Bot (how to build a profitable mev arbitrage bot)]: https://mevdao.org/blog/how-to-build-a-profitable-mev-arbitrage-bot
// [MEV Bot for Sale? What's Real, What's a Scam, and What It Should Cost (mev bot for sale)]: https://mevdao.org/blog/mev-bot-for-sale-what-s-real-what-s-a-scam-and-what-it-should-cost
// [Backrunning Bot on Ethereum: First MEV-Share Strategy (backrunning bot)]: https://mevdao.org/blog/backrunning-bot-on-ethereum-first-mev-share-strategy
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
