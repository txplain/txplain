import { useState, useEffect } from 'react'
import TransactionForm from './components/TransactionForm.js'
import ResultsDisplay from './components/ResultsDisplay.js'
import type { Network, TransactionRequest, ExplanationResult } from './types.js'

function App() {
  const [networks, setNetworks] = useState<Network[]>([])
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState<ExplanationResult | null>(null)
  const [error, setError] = useState<string>('')

  // Load networks on component mount
  useEffect(() => {
    fetchNetworks()
  }, [])

  const fetchNetworks = async () => {
    try {
      const response = await fetch('/api/v1/networks')
      if (!response.ok) throw new Error('Failed to fetch networks')
      const data = await response.json()
      setNetworks(data.networks)
    } catch (err) {
      console.error('Failed to fetch networks:', err)
      setError('Failed to load networks. Please refresh the page.')
    }
  }

  const explainTransaction = async (request: TransactionRequest) => {
    setLoading(true)
    setError('')
    setResult(null)

    // Create AbortController for request timeout and cancellation
    const controller = new AbortController()
    const timeoutId = setTimeout(() => controller.abort(), 360000) // 6 minutes (1 minute buffer over server's 5-minute timeout)

    try {
      const response = await fetch('/api/v1/explain', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(request),
        signal: controller.signal, // Add abort signal
      })

      clearTimeout(timeoutId) // Clear timeout on successful response

      if (!response.ok) {
        const errorData = await response.json()
        throw new Error(errorData.error || 'Failed to explain transaction')
      }

      const explanation = await response.json()
      setResult(explanation)
    } catch (err: unknown) {
      clearTimeout(timeoutId) // Clear timeout on error
      
      if (err instanceof Error) {
        if (err.name === 'AbortError') {
          setError('Request timed out. Complex transactions may take up to 6 minutes to analyze.')
        } else {
          setError(err.message)
        }
      } else {
        setError('An unexpected error occurred')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <div className="container mx-auto px-4 py-8">
        {/* Header */}
        <div className="text-center mb-8">
          <h1 className="text-4xl font-bold text-gray-900 mb-2">Txplain</h1>
          <p className="text-xl text-gray-600">
            AI-powered blockchain transaction explanation service
          </p>
        </div>

        {/* Main content */}
        <div className="max-w-4xl mx-auto">
          <TransactionForm
            networks={networks}
            loading={loading}
            onSubmit={explainTransaction}
          />

          {error && (
            <div className="mt-6 p-4 bg-red-50 border border-red-200 rounded-lg">
              <div className="flex">
                <div className="flex-shrink-0">
                  <svg className="h-5 w-5 text-red-400" viewBox="0 0 20 20" fill="currentColor">
                    <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clipRule="evenodd" />
                  </svg>
                </div>
                <div className="ml-3">
                  <p className="text-red-800">{error}</p>
                </div>
              </div>
            </div>
          )}

          {result && <ResultsDisplay result={result} />}
        </div>
      </div>
    </div>
  )
}

export default App
