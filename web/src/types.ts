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

export interface AddressParticipant {
  address: string
  role: string
  category: string
  type: string
  ens_name?: string
  name?: string
  icon?: string
  link?: string
  description?: string
  metadata?: Record<string, unknown>
}

export interface ExplanationResult {
  tx_hash: string
  network_id: number
  summary: string
  participants: AddressParticipant[]
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

// Progress tracking types
export type ComponentStatus = 'initiated' | 'running' | 'finished' | 'error'
export type ComponentGroup = 'data' | 'decoding' | 'enrichment' | 'analysis' | 'finishing'

export interface ComponentUpdate {
  id: string
  group: ComponentGroup
  title: string
  status: ComponentStatus
  description: string
  timestamp: string
  start_time?: string
  duration_ms?: number
  metadata?: any
}

export interface ProgressEvent {
  type: 'component_update' | 'complete' | 'error'
  component?: ComponentUpdate
  result?: any
  error?: string
  timestamp: string
} 