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
    setComponents([]) // Start with empty components, let backend drive all updates

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
      {/* Top Navigation with GitHub Link */}
      <header className="bg-white border-b border-gray-200 shadow-sm">
        <div className="container mx-auto px-4 py-3">
          <div className="flex justify-between items-center">
            <div className="flex items-center space-x-3">
              <h2 className="text-lg font-semibold text-gray-800">Txplain</h2>
              <span className="text-sm text-gray-500">Open-source AI-powered blockchain transaction analysis</span>
            </div>
            <div className="flex items-center space-x-4">
              {/* GitHub Link */}
              <a 
                href="https://github.com/txplain/txplain"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center px-3 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 hover:text-gray-900 transition-colors duration-200"
              >
                <svg className="w-4 h-4 mr-2" fill="currentColor" viewBox="0 0 20 20">
                  <path fillRule="evenodd" d="M10 0C4.477 0 0 4.484 0 10.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0110 4.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.203 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.942.359.31.678.921.678 1.856 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0020 10.017C20 4.484 15.522 0 10 0z" clipRule="evenodd" />
                </svg>
                View on GitHub
              </a>
            </div>
          </div>
        </div>
      </header>

      <div className="container mx-auto px-4 py-8">
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
