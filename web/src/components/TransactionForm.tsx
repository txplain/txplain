import { useState } from 'react'
import type { Network, TransactionRequest } from '../types'

interface TransactionFormProps {
  networks: Network[]
  loading: boolean
  onSubmit: (request: TransactionRequest) => void
}

const TransactionForm = ({ networks, loading, onSubmit }: TransactionFormProps) => {
  const [txHash, setTxHash] = useState('')
  const [networkId, setNetworkId] = useState<number>(1) // Default to Ethereum

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    
    if (!txHash.trim()) {
      alert('Please enter a transaction hash')
      return
    }
    
    onSubmit({
      tx_hash: txHash.trim(),
      network_id: networkId,
    })
  }

  const isValidTxHash = (hash: string) => {
    return /^0x[a-fA-F0-9]{64}$/.test(hash)
  }

  return (
    <div className="bg-white rounded-lg shadow-lg p-6 mb-6">
      <form onSubmit={handleSubmit} className="space-y-6">
        {/* Transaction Hash Input */}
        <div>
          <label htmlFor="txHash" className="block text-sm font-medium text-gray-700 mb-2">
            Transaction
          </label>
          <input
            type="text"
            id="txHash"
            value={txHash}
            onChange={(e) => setTxHash(e.target.value)}
            placeholder="0x1234567890abcdef..."
            className={`w-full px-3 py-2 border rounded-md shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500 ${
              txHash && !isValidTxHash(txHash) 
                ? 'border-red-300 bg-red-50' 
                : 'border-gray-300'
            }`}
            disabled={loading}
          />
          {txHash && !isValidTxHash(txHash) && (
            <p className="mt-1 text-sm text-red-600">
              Please enter a valid transaction hash (0x followed by 64 hex characters)
            </p>
          )}
        </div>

        {/* Network Selection */}
        <div>
          <label htmlFor="network" className="block text-sm font-medium text-gray-700 mb-2">
            Network
          </label>
          <select
            id="network"
            value={networkId}
            onChange={(e) => setNetworkId(Number(e.target.value))}
            className="w-full px-3 py-2 border border-gray-300 rounded-md shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            disabled={loading}
          >
            {networks.map((network) => (
              <option key={network.id} value={network.id}>
                {network.name} (Chain ID: {network.id})
              </option>
            ))}
          </select>
        </div>

        {/* Submit Button */}
        <div>
          <button
            type="submit"
            disabled={loading || !txHash.trim() || !isValidTxHash(txHash)}
            className={`w-full flex justify-center py-2 px-4 border border-transparent rounded-md shadow-sm text-sm font-medium text-white ${
              loading || !txHash.trim() || !isValidTxHash(txHash)
                ? 'bg-gray-400 cursor-not-allowed'
                : 'bg-blue-600 hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500'
            } transition-colors duration-200`}
          >
            {loading ? (
              <div className="flex items-center">
                                  <svg
                    className="animate-spin -ml-1 mr-3 h-5 w-5 text-white"
                    xmlns="http://www.w3.org/2000/svg"
                    fill="none"
                    viewBox="0 0 24 24"
                  >
                    <circle
                      className="opacity-25"
                      cx="12"
                      cy="12"
                      r="10"
                      stroke="currentColor"
                      strokeWidth="4"
                    ></circle>
                    <path
                      className="opacity-75"
                      fill="currentColor"
                      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                    ></path>
                  </svg>
                  Analyzing...
              </div>
            ) : (
              'Explain'
            )}
          </button>
        </div>
      </form>

      {/* Examples */}
      <div className="mt-8 border-t border-gray-200 pt-6">
        <h3 className="text-sm font-medium text-gray-700 mb-3">Examples:</h3>
        <div className="space-y-2">
          <button
            type="button"
            onClick={() => setTxHash('0x85a909e8d6d173768afa9dcb3116f88ecf25a8af884b078d02b3ad0a7167f998')}
            className="text-sm text-blue-600 hover:text-blue-800 block truncate max-w-full"
            disabled={loading}
          >
            0x85a909e8...7167f998 (Token Approval)
          </button>
          <button
            type="button"
            onClick={() => setTxHash('0xed21b60a115828a7bdaaa6d22309e3a5ba47375b926d18fa8e5768a1d65458e0')}
            className="text-sm text-blue-600 hover:text-blue-800 block truncate max-w-full"
            disabled={loading}
          >
            0xed21b60a...d65458e0 (Token Swap)
          </button>
          <button
            type="button"
            onClick={() => setTxHash('0x0824267bef6fc363ed974c5c25f3856b06e5beaa434e12100df01ba22056b1b2')}
            className="text-sm text-blue-600 hover:text-blue-800 block truncate max-w-full"
            disabled={loading}
          >
            0x0824267b...22056b1b2 (Access Control)
          </button>
          <button
            type="button"
            onClick={() => setTxHash('0x34a2d414c9a37d86c0371cba3b17bf3caa5da129efb239a571f3c8629518c227')}
            className="text-sm text-blue-600 hover:text-blue-800 block truncate max-w-full"
            disabled={loading}
          >
            0x34a2d414...29518c227 (Bridge Deposit)
          </button>
          <button
            type="button"
            onClick={() => setTxHash('0xc2d06d1d2da6ecfa2b500e821852b8ced9d4782098d62f3d954c90fcd89fce64')}
            className="text-sm text-blue-600 hover:text-blue-800 block truncate max-w-full"
            disabled={loading}
          >
            0xc2d06d1d...fcd89fce64 (Cross-chain Verification)
          </button>
          <button
            type="button"
            onClick={() => setTxHash('0x5035ce80e4963588f8a3eb47ffe866cd64bff1960fa206595e5658546adf558f')}
            className="text-sm text-blue-600 hover:text-blue-800 block truncate max-w-full"
            disabled={loading}
          >
            0x5035ce80...6adf558f (Batch Order Fulfillment)
          </button>
        </div>
      </div>
    </div>
  )
}

export default TransactionForm 