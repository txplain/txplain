import type { ExplanationResult } from '../types'
import AnnotatedText from './AnnotatedText'

interface ResultsDisplayProps {
  result: ExplanationResult
}

const ResultsDisplay = ({ result }: ResultsDisplayProps) => {
  const formatAddress = (address: string) => {
    if (!address || address.length < 10) {
      return '...'
    }
    return `${address.slice(0, 6)}...${address.slice(-4)}`
  }

  const formatAmount = (amount: string, symbol?: string) => {
    if (!amount || amount === '0x' || amount === '0x0') return '0'
    
    // Handle hex amounts
    if (amount.startsWith('0x')) {
      try {
        const hexValue = amount.slice(2)
        // Check if it's all zeros
        if (hexValue.replace(/0/g, '') === '') return '0'
        
        // Convert hex to decimal
        const num = parseInt(hexValue, 16)
        if (num === 0) return '0'
        
        // For display, just show the decimal value
        if (symbol) {
          return `${num} ${symbol}`
        }
        return num.toString()
      } catch {
        return '0'
      }
    }
    
    // Convert from wei if needed (simple approach)
    try {
      const num = parseFloat(amount)
      if (num === 0) return '0'
      
      if (symbol) {
        return `${amount} ${symbol}`
      }
      return amount
    } catch {
      return amount || '0'
    }
  }

  const getStatusColor = (status: string) => {
    switch (status.toLowerCase()) {
      case 'success':
        return 'text-green-600 bg-green-50'
      case 'failed':
      case 'reverted':
        return 'text-red-600 bg-red-50'
      default:
        return 'text-gray-600 bg-gray-50'
    }
  }

  return (
    <div className="mt-6 space-y-6">
      {/* Main Summary */}
      <div className="bg-white rounded-lg shadow-lg p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-xl font-semibold text-gray-900">Transaction Summary</h2>
          <span className={`px-3 py-1 rounded-full text-sm font-medium ${getStatusColor(result.status)}`}>
            {result.status.charAt(0).toUpperCase() + result.status.slice(1)}
          </span>
        </div>
        
        <div className="prose max-w-none">
          <p className="text-lg text-gray-700 leading-relaxed">
            <AnnotatedText 
              text={result.summary} 
              annotations={result.annotations || []} 
            />
          </p>
        </div>

        {/* Transaction Details */}
        <div className="mt-6 grid grid-cols-1 md:grid-cols-3 gap-4 pt-4 border-t border-gray-200">
          <div>
            <dt className="text-sm font-medium text-gray-500">Gas Used</dt>
            <dd className="mt-1 text-sm text-gray-900">{result.gas_used.toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-sm font-medium text-gray-500">Transaction Fee</dt>
            <dd className="mt-1 text-sm text-gray-900">{result.tx_fee}</dd>
          </div>
          <div>
            <dt className="text-sm font-medium text-gray-500">Block</dt>
            <dd className="mt-1 text-sm text-gray-900">{result.block_number.toLocaleString()}</dd>
          </div>
        </div>
      </div>

      {/* Token Transfers */}
      {result.transfers && result.transfers.length > 0 && (
        <div className="bg-white rounded-lg shadow-lg p-6">
          <h3 className="text-lg font-semibold text-gray-900 mb-4">Token Transfers</h3>
          <div className="space-y-3">
            {result.transfers
              .filter(transfer => {
                // Filter out transfers with no meaningful data
                if (!transfer.from || !transfer.to || transfer.from === '...' || transfer.to === '...') {
                  return false
                }
                
                // Filter out zero amounts (but keep NFTs)
                if (transfer.type === 'ERC20' && (!transfer.formatted_amount || transfer.formatted_amount === '0')) {
                  const rawAmount = formatAmount(transfer.amount, transfer.symbol)
                  if (!rawAmount || rawAmount === '0') {
                    return false
                  }
                }
                
                return true
              })
              .map((transfer, index) => (
              <div key={index} className="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
                <div className="flex-1">
                  <div className="flex items-center space-x-2">
                    <span className="text-sm font-medium text-gray-600">{transfer.type}</span>
                    {transfer.symbol && (
                      <span className="px-2 py-1 text-xs bg-blue-100 text-blue-800 rounded">
                        {transfer.symbol}
                      </span>
                    )}
                    {transfer.name && transfer.name !== transfer.symbol && (
                      <span className="text-xs text-gray-600">({transfer.name})</span>
                    )}
                  </div>
                  <div className="mt-1 text-sm text-gray-500">
                    From {formatAddress(transfer.from)} → To {formatAddress(transfer.to)}
                  </div>
                </div>
                <div className="text-right">
                  <div className="text-sm font-medium text-gray-900">
                    {transfer.formatted_amount ? 
                      `${transfer.formatted_amount} ${transfer.symbol || ''}` : 
                      formatAmount(transfer.amount, transfer.symbol)
                    }
                  </div>
                  {transfer.amount_usd && (
                    <div className="text-xs text-gray-500">${transfer.amount_usd}</div>
                  )}
                </div>
              </div>
            ))}
          </div>
          {result.transfers.filter(transfer => {
            if (!transfer.from || !transfer.to || transfer.from === '...' || transfer.to === '...') {
              return false
            }
            if (transfer.type === 'ERC20' && (!transfer.formatted_amount || transfer.formatted_amount === '0')) {
              const rawAmount = formatAmount(transfer.amount, transfer.symbol)
              if (!rawAmount || rawAmount === '0') {
                return false
              }
            }
            return true
          }).length === 0 && (
            <div className="text-center text-gray-500 py-4">
              No significant token transfers to display
            </div>
          )}
        </div>
      )}



      {/* Risks & Warnings */}
      {result.risks && result.risks.length > 0 && (
        <div className="bg-yellow-50 border border-yellow-200 rounded-lg p-6">
          <h3 className="text-lg font-semibold text-yellow-800 mb-3">⚠️ Risks & Warnings</h3>
          <ul className="space-y-2">
            {result.risks.map((risk, index) => (
              <li key={index} className="text-sm text-yellow-700 flex items-start">
                <span className="inline-block w-2 h-2 bg-yellow-400 rounded-full mt-2 mr-3 flex-shrink-0"></span>
                {risk}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Tags */}
      {result.tags && result.tags.length > 0 && (
        <div className="bg-white rounded-lg shadow-lg p-6">
          <h3 className="text-lg font-semibold text-gray-900 mb-3">Tags</h3>
          <div className="flex flex-wrap gap-2">
            {result.tags.map((tag, index) => (
              <span
                key={index}
                className="px-3 py-1 text-sm bg-blue-100 text-blue-800 rounded-full"
              >
                {tag}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Links */}
      {result.links && Object.keys(result.links).length > 0 && (
        <div className="bg-white rounded-lg shadow-lg p-6">
          <h3 className="text-lg font-semibold text-gray-900 mb-3">Related Links</h3>
          <div className="space-y-2">
            {Object.entries(result.links).map(([name, url], index) => (
              <div key={index}>
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-blue-600 hover:text-blue-800 text-sm flex items-center"
                >
                  {name}
                  <svg
                    className="w-4 h-4 ml-1"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                    xmlns="http://www.w3.org/2000/svg"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
                    />
                  </svg>
                </a>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

export default ResultsDisplay 