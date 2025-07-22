// API Types matching Go models
export interface Network {
  id: number
  name: string
  rpc_url: string
  explorer: string
}

export interface TransactionRequest {
  tx_hash: string
  network_id: number
}

export interface TokenTransfer {
  type: string
  contract?: string
  from: string
  to: string
  amount: string
  token_id?: string
  symbol?: string
  name?: string
  decimals?: number
  formatted_amount?: string
  amount_usd?: string
}

export interface Annotation {
  text: string
  link?: string
  tooltip?: string
  icon?: string
}

export interface ExplanationResult {
  tx_hash: string
  network_id: number
  summary: string
  transfers: TokenTransfer[]
  gas_used: number
  gas_price: string
  tx_fee: string
  status: string
  timestamp: string
  block_number: number
  links: Record<string, string>
  risks?: string[]
  tags?: string[]
  metadata?: Record<string, unknown>
  annotations?: Annotation[]
} 