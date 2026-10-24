import "dotenv/config";
import {
  createPublicClient,
  createWalletClient,
  encodeFunctionData,
  http,
  keccak256,
  parseAbi,
  parseGwei,
  toBytes,
  type Address,
  type Hex,
} from "viem";
import { privateKeyToAccount } from "viem/accounts";
import { mainnet } from "viem/chains";

/**
 * MEV DAO Atomic 3-Transaction Airdrop Claim & Rescue Bundle
 * Full Technical Guide:
 * https://mevdao.org/blog/how-to-claim-an-airdrop-from-a-compromised-wallet-before-the-sweeper-does
 *
 * Bundle Sequence:
 *   Tx #1 (sponsorWallet)     -> Sends exact ETH gas to compromisedWallet
 *   Tx #2 (compromisedWallet) -> Calls AirdropDistributor.claim(...)
 *   Tx #3 (compromisedWallet) -> Transfers claimed ERC-20 tokens to safeRecipient
 */

const ERC20_ABI = parseAbi([
  "function transfer(address to, uint256 amount) returns (bool)",
]);

export async function buildAtomicAirdropRescueBundle(params: {
  rpcUrl: string;
  sponsorPk: Hex;
  compromisedPk: Hex;
  safeRecipient: Address;
  distributorAddress: Address;
  claimCalldata: Hex;
  tokenAddress: Address;
  claimAmount: bigint;
  claimGasLimit?: bigint;
  transferGasLimit?: bigint;
  priorityFeeGwei?: string;
}) {
  const sponsorAccount = privateKeyToAccount(params.sponsorPk);
  const compromisedAccount = privateKeyToAccount(params.compromisedPk);

  const publicClient = createPublicClient({
    chain: mainnet,
    transport: http(params.rpcUrl),
  });
  const sponsorClient = createWalletClient({
    account: sponsorAccount,
    chain: mainnet,
    transport: http(params.rpcUrl),
  });
  const compromisedClient = createWalletClient({
    account: compromisedAccount,
    chain: mainnet,
    transport: http(params.rpcUrl),
  });

  const [block, sponsorNonce, compromisedNonce] = await Promise.all([
    publicClient.getBlock(),
    publicClient.getTransactionCount({ address: sponsorAccount.address }),
    publicClient.getTransactionCount({ address: compromisedAccount.address }),
  ]);

  const priorityFeePerGas = parseGwei(params.priorityFeeGwei || "20");
  const maxFeePerGas = (block.baseFeePerGas ?? parseGwei("15")) * 2n + priorityFeePerGas;

  const claimGas = params.claimGasLimit ?? 140_000n;
  const transferGas = params.transferGasLimit ?? 65_000n;
  const totalCompromisedGas = claimGas + transferGas;
  const exactSponsorFundingWei = totalCompromisedGas * maxFeePerGas;

  // Tx #1: Sponsor funds exact gas for BOTH Tx #2 (claim) and Tx #3 (transfer)
  const tx1SponsorFund = await sponsorClient.signTransaction({
    account: sponsorAccount,
    to: compromisedAccount.address,
    value: exactSponsorFundingWei,
    gas: 21_000n,
    maxFeePerGas,
    maxPriorityFeePerGas: priorityFeePerGas,
    nonce: sponsorNonce,
  });

  // Tx #2: Compromised wallet executes the Merkle airdrop claim
  const tx2ClaimAirdrop = await compromisedClient.signTransaction({
    account: compromisedAccount,
    to: params.distributorAddress,
    data: params.claimCalldata,
    value: 0n,
    gas: claimGas,
    maxFeePerGas,
    maxPriorityFeePerGas: priorityFeePerGas,
    nonce: compromisedNonce,
  });

  // Tx #3: Compromised wallet immediately transfers the newly minted/claimed tokens to safeRecipient
  const transferData = encodeFunctionData({
    abi: ERC20_ABI,
    functionName: "transfer",
    args: [params.safeRecipient, params.claimAmount],
  });

  const tx3TransferTokens = await compromisedClient.signTransaction({
    account: compromisedAccount,
    to: params.tokenAddress,
    data: transferData,
    value: 0n,
    gas: transferGas,
    maxFeePerGas,
    maxPriorityFeePerGas: priorityFeePerGas,
    nonce: compromisedNonce + 1,
  });

  const bundleBody = JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "eth_sendBundle",
    params: [
      {
        txs: [tx1SponsorFund, tx2ClaimAirdrop, tx3TransferTokens],
        blockNumber: `0x${((block.number ?? 0n) + 1n).toString(16)}`,
      },
    ],
  });

  const signature = await sponsorAccount.signMessage({
    message: keccak256(toBytes(bundleBody)),
  });

  return {
    targetBlock: (block.number ?? 0n) + 1n,
    exactSponsorFundingWei,
    txs: [tx1SponsorFund, tx2ClaimAirdrop, tx3TransferTokens],
    flashbotsAuthHeader: `${sponsorAccount.address}:${signature}`,
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  console.log("MEV DAO Airdrop Rescue Builder loaded. Configure .env or import buildAtomicAirdropRescueBundle().");
  console.log("Guide: https://mevdao.org/blog/how-to-claim-an-airdrop-from-a-compromised-wallet-before-the-sweeper-does");
}
