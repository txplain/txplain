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

  // Deterministic color generation based on text hash
  const getColorFromText = (text: string, colorType: 'bg' | 'text' = 'bg') => {
    let hash = 0
    for (let i = 0; i < text.length; i++) {
      const char = text.charCodeAt(i)
      hash = ((hash << 5) - hash) + char
      hash = hash & hash // Convert to 32bit integer
    }
    
    const bgColors = [
      'bg-red-500', 'bg-blue-500', 'bg-green-800', 'bg-yellow-500',
      'bg-purple-500', 'bg-pink-500', 'bg-indigo-500', 'bg-orange-500',
      'bg-emerald-500', 'bg-teal-500'
    ]
    
    const textColors = [
      'text-red-700', 'text-blue-700', 'text-green-700', 'text-yellow-700',
      'text-purple-700', 'text-pink-700', 'text-indigo-700', 'text-orange-700',
      'text-emerald-700', 'text-teal-700'
    ]
    
    // Special case for transaction to keep it purple for consistency
    if (text === 'transaction') {
      return colorType === 'bg' ? 'bg-purple-500' : 'text-purple-700'
    }
    
    const colors = colorType === 'bg' ? bgColors : textColors
    return colors[Math.abs(hash) % colors.length]
  }

  const formatAmount = (amount: string, symbol?: string) => {
    if (!amount || amount === '0x' || amount === '0x0') return '0'
    
    // Handle hex amounts
    if (amount.startsWith('0x')) {
      try {
        const hexValue = amount.slice(2)
        // Check if it's all zeros
        if (hexValue.replace(/0/g, '') === '') return '0'
        
        // Use BigInt for large numbers instead of parseInt
        const bigNum = BigInt('0x' + hexValue)
        if (bigNum === BigInt(0)) return '0'
        
        // For display, just show the decimal value (raw amount, no decimals applied)
        const numStr = bigNum.toString()
        if (symbol) {
          return `${numStr} ${symbol}`
        }
        return numStr
      } catch {
        return '0'
      }
    }
    
    // Handle regular numeric strings
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
          <h2 className="text-xl font-semibold text-gray-900">Summary</h2>
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
        <div className="mt-6 grid grid-cols-1 md:grid-cols-2 gap-4 pt-4 border-t border-gray-200">
          <div>
            <dt className="text-sm font-medium text-gray-500">Gas Used</dt>
            <dd className="mt-1 text-sm text-gray-900">{result.gas_used.toLocaleString()}</dd>
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
                // Always keep transfers with valid from/to addresses
                if (!transfer.from || !transfer.to || transfer.from === '...' || transfer.to === '...') {
                  return false
                }
                
                // Keep NFTs (ERC721/ERC1155) regardless of amount
                if (transfer.type === 'ERC721' || transfer.type === 'ERC1155' || transfer.token_id) {
                  return true
                }
                
                // For ERC20 tokens, check if we have meaningful amount data
                if (transfer.type === 'ERC20') {
                  // Keep if we have a valid formatted amount
                  if (transfer.formatted_amount && transfer.formatted_amount !== '0' && transfer.formatted_amount !== '') {
                    return true
                  }
                  
                  // Keep if we have a valid raw amount
                  if (transfer.amount && transfer.amount !== '0x' && transfer.amount !== '0x0' && transfer.amount !== '') {
                    const rawAmount = formatAmount(transfer.amount, transfer.symbol)
                    if (rawAmount && rawAmount !== '0' && rawAmount !== '') {
                      return true
                    }
                  }
                  
                  // Keep if we have symbol/name (might be approval or other operation)
                  if (transfer.symbol || transfer.name) {
                    return true
                  }
                }
                
                // Keep by default (better to show too much than too little)
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
                    {(() => {
                      // Prioritize formatted_amount from backend processing
                      if (transfer.formatted_amount && transfer.formatted_amount !== '0') {
                        return `${transfer.formatted_amount} ${transfer.symbol || ''}`;
                      }
                      
                      // Fallback to manual formatting of raw amount
                      if (transfer.amount && transfer.amount !== '0x' && transfer.amount !== '0x0') {
                        const formatted = formatAmount(transfer.amount, transfer.symbol);
                        if (formatted && formatted !== '0') {
                          return formatted;
                        }
                      }
                      
                      // Show token ID for NFTs
                      if (transfer.token_id) {
                        return `Token ID: ${transfer.token_id}`;
                      }
                      
                      // Last resort fallback
                      return `${transfer.symbol || 'Unknown'}`;
                    })()}
                  </div>
                  {transfer.amount_usd && transfer.amount_usd !== '0' && (
                    <div className="text-xs text-gray-500">${transfer.amount_usd}</div>
                  )}
                </div>
              </div>
            ))}
          </div>
          {result.transfers.filter(transfer => {
            // Use same filtering logic as above for consistency
            if (!transfer.from || !transfer.to || transfer.from === '...' || transfer.to === '...') {
              return false
            }
            
            if (transfer.type === 'ERC721' || transfer.type === 'ERC1155' || transfer.token_id) {
              return true
            }
            
            if (transfer.type === 'ERC20') {
              if (transfer.formatted_amount && transfer.formatted_amount !== '0' && transfer.formatted_amount !== '') {
                return true
              }
              
              if (transfer.amount && transfer.amount !== '0x' && transfer.amount !== '0x0' && transfer.amount !== '') {
                const rawAmount = formatAmount(transfer.amount, transfer.symbol)
                if (rawAmount && rawAmount !== '0' && rawAmount !== '') {
                  return true
                }
              }
              
              if (transfer.symbol || transfer.name) {
                return true
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

      {/* Transaction Participants */}
      {result.participants && result.participants.length > 0 && (
        <div className="bg-white rounded-lg shadow-lg p-6">
          <h3 className="text-lg font-semibold text-gray-900 mb-4">Participants</h3>
          <div className="space-y-3">
            {result.participants.map((participant, index) => {
              // Deterministic color assignment based on category name hash
              const getCategoryColor = (cat: string) => getColorFromText(cat, 'bg')
              
              // Deterministic text color based on category
              const getCategoryTextColor = (cat: string) => getColorFromText(cat, 'text')
              
              return (
                <div key={index} className="flex items-center justify-between p-3 bg-gray-50 rounded-lg hover:bg-gray-100 transition-colors">
                  <div className="flex items-center space-x-3">
                    {/* Category Icon */}
                    <div className={`flex-shrink-0 w-2 h-2 rounded-full ${getCategoryColor(participant.category)}`}></div>
                    
                    {/* Participant Info */}
                    <div className="flex-1">
                      <div className={`font-medium ${getCategoryTextColor(participant.category)}`}>
                        {participant.role}
                      </div>
                      <div className="text-xs text-gray-500 font-mono">
                        {formatAddress(participant.address)}
                        {participant.ens_name && (
                          <span className="ml-2 text-blue-600 font-sans">
                            ({participant.ens_name})
                          </span>
                        )}
                      </div>
                      {participant.name && (
                        <div className="text-xs text-gray-600 mt-1">
                          {participant.name}
                        </div>
                      )}
                      <div className="text-xs text-gray-400 mt-1">
                        {participant.type} • {participant.category}
                      </div>
                    </div>
                  </div>
                  
                  {/* Link Button */}
                  {participant.link && (
                    <a
                      href={participant.link}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="flex-shrink-0 inline-flex items-center px-3 py-1.5 text-xs font-medium text-blue-600 bg-blue-50 rounded-md hover:bg-blue-100 hover:text-blue-700 transition-colors"
                    >
                      View
                      <svg
                        className="w-3 h-3 ml-1"
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
                  )}
                </div>
              )
            })}
          </div>
          
          {/* Category Legend */}
          <div className="mt-4 pt-3 border-t border-gray-200">
            <div className="flex flex-wrap gap-4 text-xs text-gray-600">
              {/* Show all unique categories from participants */}
              {[...new Set(result.participants.map(p => p.category))].map((category) => {
                const getCategoryColor = (cat: string) => getColorFromText(cat, 'bg')
                
                return (
                  <div key={category} className="flex items-center space-x-1">
                    <div className={`w-2 h-2 rounded-full ${getCategoryColor(category)}`}></div>
                    <span className="capitalize">{category}</span>
                  </div>
                )
              })}
            </div>
          </div>
        </div>
      )}

      {/* Fallback: Address Roles & Links (for backward compatibility) */}
      {(!result.participants || result.participants.length === 0) && result.links && Object.keys(result.links).length > 0 && (
        <div className="bg-white rounded-lg shadow-lg p-6">
          <h3 className="text-lg font-semibold text-gray-900 mb-4">Transaction Links</h3>
          <div className="space-y-3">
            {Object.entries(result.links).map(([role, url], index) => (
              <div key={index} className="flex items-center justify-between p-3 bg-gray-50 rounded-lg hover:bg-gray-100 transition-colors">
                <div className="flex items-center space-x-3">
                  <div className="flex-shrink-0 w-2 h-2 rounded-full bg-gray-400"></div>
                  <div className="flex-1">
                    <div className="font-medium text-gray-900">{role}</div>
                  </div>
                </div>
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex-shrink-0 inline-flex items-center px-3 py-1.5 text-xs font-medium text-blue-600 bg-blue-50 rounded-md hover:bg-blue-100 hover:text-blue-700 transition-colors"
                >
                  View
                  <svg
                    className="w-3 h-3 ml-1"
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