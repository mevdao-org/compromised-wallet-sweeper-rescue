import "dotenv/config";
import {
  createPublicClient,
  createWalletClient,
  encodeFunctionData,
  formatEther,
  formatUnits,
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
 * MEV DAO Whitehat Sweeper Bot Rescue Script
 * Website: https://mevdao.org
 * Step-by-Step Guide:
 * https://mevdao.org/blog/how-to-rescue-tokens-from-a-compromised-wallet-with-a-sweeper-bot-flashbots-guid
 * Managed Emergency Rescue Service:
 * https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
 */

const ERC20_ABI = parseAbi([
  "function balanceOf(address owner) view returns (uint256)",
  "function decimals() view returns (uint8)",
  "function symbol() view returns (string)",
  "function transfer(address to, uint256 amount) returns (bool)",
]);

const BUILDER_RELAYS = [
  { name: "Flashbots", url: "https://relay.flashbots.net" },
  { name: "Beaverbuild", url: "https://rpc.beaverbuild.org" },
  { name: "Titan Builder", url: "https://rpc.titanbuilder.xyz" },
  { name: "Rsync Builder", url: "https://rsync-builder.xyz" },
] as const;

async function signFlashbotsBody(body: string, signerPk: Hex): Promise<string> {
  const account = privateKeyToAccount(signerPk);
  const digest = keccak256(toBytes(body));
  const sig = await account.signMessage({ message: digest });
  return `${account.address}:${sig}`;
}

async function main() {
  const rpcUrl = process.env.ETH_HTTP_RPC_URL || "https://eth.llamarpc.com";
  const sponsorPk = (process.env.SPONSOR_PRIVATE_KEY ||
    "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80") as Hex;
  const compromisedPk = (process.env.COMPROMISED_PRIVATE_KEY ||
    "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d") as Hex;
  const safeRecipient = (process.env.SAFE_RECIPIENT_ADDRESS ||
    "0x000000000000000000000000000000000000dEaD") as Address;
  const tokenAddress = (process.env.TOKEN_CONTRACT_ADDRESS ||
    "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48") as Address;

  const priorityFeePerGas = parseGwei(process.env.PRIORITY_FEE_GWEI || "15");

  const sponsorAccount = privateKeyToAccount(sponsorPk);
  const compromisedAccount = privateKeyToAccount(compromisedPk);

  const publicClient = createPublicClient({
    chain: mainnet,
    transport: http(rpcUrl),
  });
  const sponsorClient = createWalletClient({
    account: sponsorAccount,
    chain: mainnet,
    transport: http(rpcUrl),
  });
  const compromisedClient = createWalletClient({
    account: compromisedAccount,
    chain: mainnet,
    transport: http(rpcUrl),
  });

  console.log("===================================================================");
  console.log("  MEV DAO Atomic Flashbots Sweeper Rescue (https://mevdao.org)");
  console.log("===================================================================");
  console.log(`Sponsor Wallet (Clean)     : ${sponsorAccount.address}`);
  console.log(`Compromised Wallet         : ${compromisedAccount.address}`);
  console.log(`Safe Destination Vault     : ${safeRecipient}`);
  console.log(`Target ERC-20 Contract     : ${tokenAddress}`);

  const [block, tokenBalance, decimals, symbol, sponsorNonce, compromisedNonce] =
    await Promise.all([
      publicClient.getBlock(),
      publicClient.readContract({
        address: tokenAddress,
        abi: ERC20_ABI,
        functionName: "balanceOf",
        args: [compromisedAccount.address],
      }),
      publicClient
        .readContract({ address: tokenAddress, abi: ERC20_ABI, functionName: "decimals" })
        .catch(() => 18),
      publicClient
        .readContract({ address: tokenAddress, abi: ERC20_ABI, functionName: "symbol" })
        .catch(() => "ERC20"),
      publicClient.getTransactionCount({ address: sponsorAccount.address }),
      publicClient.getTransactionCount({ address: compromisedAccount.address }),
    ]);

  console.log(
    `Detected ${symbol} Balance    : ${formatUnits(tokenBalance, decimals)} ${symbol} (Block #${block.number})`
  );

  if (tokenBalance === 0n) {
    console.log(
      "\n[NOTICE] Compromised wallet holds 0 of the specified token. Update TOKEN_CONTRACT_ADDRESS in .env to run a live rescue."
    );
    return;
  }

  // Calculate exact EIP-1559 gas parameters so the compromised wallet spends 100% of funded ETH
  const baseFee = block.baseFeePerGas ?? parseGwei("15");
  const maxFeePerGas = baseFee * 2n + priorityFeePerGas;

  const transferData = encodeFunctionData({
    abi: ERC20_ABI,
    functionName: "transfer",
    args: [safeRecipient, tokenBalance],
  });

  // Estimate exact gas required for the ERC-20 transfer
  const estimatedTransferGas = await publicClient
    .estimateGas({
      account: compromisedAccount.address,
      to: tokenAddress,
      data: transferData,
    })
    .catch(() => 65_000n);

  const exactEthFundingRequired = estimatedTransferGas * maxFeePerGas;
  console.log(
    `Exact Gas Funding Required : ${formatEther(exactEthFundingRequired)} ETH (${estimatedTransferGas} gas @ maxFee ${formatUnits(maxFeePerGas, 9)} gwei)`
  );

  // TX #1: Sponsor wallet sends exact ETH needed for gas to Compromised Wallet
  const signedSponsorTx = await sponsorClient.signTransaction({
    account: sponsorAccount,
    to: compromisedAccount.address,
    value: exactEthFundingRequired,
    gas: 21_000n,
    maxFeePerGas,
    maxPriorityFeePerGas: priorityFeePerGas,
    nonce: sponsorNonce,
  });

  // TX #2: Compromised wallet transfers 100% of ERC-20 tokens to Safe Recipient in the SAME block
  const signedRescueTx = await compromisedClient.signTransaction({
    account: compromisedAccount,
    to: tokenAddress,
    data: transferData,
    value: 0n,
    gas: estimatedTransferGas,
    maxFeePerGas,
    maxPriorityFeePerGas: priorityFeePerGas,
    nonce: compromisedNonce,
  });

  const targetBlock = (block.number ?? 0n) + 1n;

  // Step 1: Simulate bundle via eth_callBundle before broadcasting
  const callBundlePayload = JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "eth_callBundle",
    params: [
      {
        txs: [signedSponsorTx, signedRescueTx],
        blockNumber: `0x${targetBlock.toString(16)}`,
        stateBlockNumber: "latest",
      },
    ],
  });

  const simSig = await signFlashbotsBody(callBundlePayload, sponsorPk);
  const simRes = await fetch("https://relay.flashbots.net", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Flashbots-Signature": simSig,
    },
    body: callBundlePayload,
  });
  const simJson = (await simRes.json()) as any;

  if (simJson.error || simJson.result?.results?.some((r: any) => r.error || r.revert)) {
    console.error("[SIMULATION FAILED] Bundle reverted during eth_callBundle:", JSON.stringify(simJson, null, 2));
    process.exit(1);
  }

  console.log(
    `[SIMULATION PASSED] Both txs succeeded! Effective Bundle Gas Price: ${simJson.result?.bundleGasPrice} wei`
  );

  // Step 2: Fan out atomic rescue bundle across next 3 blocks to all major builders
  for (let offset = 1n; offset <= 3n; offset++) {
    const candidateBlock = (block.number ?? 0n) + offset;
    const sendPayload = JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "eth_sendBundle",
      params: [
        {
          txs: [signedSponsorTx, signedRescueTx],
          blockNumber: `0x${candidateBlock.toString(16)}`,
        },
      ],
    });
    const sendSig = await signFlashbotsBody(sendPayload, sponsorPk);

    const results = await Promise.all(
      BUILDER_RELAYS.map(async (relay) => {
        const res = await fetch(relay.url, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Flashbots-Signature": sendSig,
          },
          body: sendPayload,
        });
        const data = (await res.json()) as any;
        return {
          builder: relay.name,
          targetBlock: candidateBlock.toString(),
          bundleHash: data.result?.bundleHash || data.error?.message,
        };
      })
    );
    console.table(results);
  }
}

main().catch((err) => {
  console.error("Fatal error:", err);
  process.exit(1);
});
