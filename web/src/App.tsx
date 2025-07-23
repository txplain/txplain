import { useState, useEffect } from 'react'
import TransactionForm from './components/TransactionForm.js'
import ResultsDisplay from './components/ResultsDisplay.js'
import ProgressDisplay from './components/ProgressDisplay.js'
import type { Network, TransactionRequest, ExplanationResult, ComponentUpdate, ProgressEvent } from './types.js'

function App() {
  const [networks, setNetworks] = useState<Network[]>([])
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState<ExplanationResult | null>(null)
  const [error, setError] = useState<string>('')
  const [components, setComponents] = useState<ComponentUpdate[]>([])

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
    setComponents([
      {
        id: 'initialization',
        group: 'data',
        title: 'Starting Analysis', 
        status: 'running',
        description: 'Initializing transaction analysis...',
        timestamp: new Date().toISOString(),
        start_time: new Date().toISOString(),
        duration_ms: 0
      }
    ])

    try {
      // Use fetch with SSE streaming instead of EventSource (which doesn't support POST)
      const response = await fetch('/api/v1/explain-sse', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(request),
      })

      if (!response.ok) {
        throw new Error('Failed to start analysis')
      }

      const reader = response.body?.getReader()
      const decoder = new TextDecoder()

      if (!reader) {
        throw new Error('No response body')
      }

      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        
        // Keep the last incomplete line in buffer
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const eventData = JSON.parse(line.slice(6)) as ProgressEvent
              
              if (eventData.type === 'component_update' && eventData.component) {
                setComponents(prev => {
                  const existing = prev.find(c => c.id === eventData.component!.id)
                  if (existing) {
                    return prev.map(c => c.id === eventData.component!.id ? eventData.component! : c)
                  } else {
                    return [...prev, eventData.component!]
                  }
                })
              } else if (eventData.type === 'complete' && eventData.result) {
                setResult(eventData.result as ExplanationResult)
                setLoading(false)
                return
              } else if (eventData.type === 'error') {
                setError(eventData.error || 'Analysis failed')
                setLoading(false)
                return
              }
            } catch (e) {
              console.error('Failed to parse SSE data:', e)
            }
          }
        }
      }
    } catch (err: unknown) {
      if (err instanceof Error) {
        setError(err.message)
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

          {/* Progress Display */}
          {(loading || components.length > 0) && (
            <ProgressDisplay 
              components={components} 
              isComplete={!loading && result !== null}
            />
          )}

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
