import { useState, useEffect, useRef } from 'react'
import TransactionForm from './components/TransactionForm.js'
import ResultsDisplay from './components/ResultsDisplay.js'
import ProgressDisplay from './components/ProgressDisplay.js'
import type { Network, TransactionRequest, ExplanationResult, ComponentUpdate, ProgressEvent } from './types.js'

// Enhanced error types for better user experience
interface ErrorState {
  message: string
  type: 'server' | 'network' | 'timeout' | 'connection' | 'unknown'
  isRetryable: boolean
  details?: string
}

function App() {
  const [networks, setNetworks] = useState<Network[]>([])
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState<ExplanationResult | null>(null)
  const [error, setError] = useState<ErrorState | null>(null)
  const [components, setComponents] = useState<ComponentUpdate[]>([])
  const [isOnline, setIsOnline] = useState(navigator.onLine)
  const [lastRequest, setLastRequest] = useState<TransactionRequest | null>(null) // Store last request for retry
  const [retryCount, setRetryCount] = useState(0) // Track retry attempts
  const progressDisplayRef = useRef<HTMLDivElement>(null)

  // Monitor online/offline status
  useEffect(() => {
    const handleOnline = () => setIsOnline(true)
    const handleOffline = () => setIsOnline(false)
    
    window.addEventListener('online', handleOnline)
    window.addEventListener('offline', handleOffline)
    
    return () => {
      window.removeEventListener('online', handleOnline)
      window.removeEventListener('offline', handleOffline)
    }
  }, [])

  // Load networks on component mount
  useEffect(() => {
    fetchNetworks()
  }, [])

  const createErrorState = (message: string, type: ErrorState['type'], isRetryable: boolean = true, details?: string): ErrorState => {
    return { message, type, isRetryable, details }
  }

  const handleError = (err: unknown, context: string = '', failedComponent?: string): ErrorState => {
    console.error(`[ERROR] ${context}:`, err)
    
    if (!navigator.onLine) {
      return createErrorState(
        'No internet connection detected. Please check your network and try again.',
        'network',
        true,
        'Your device appears to be offline. Check your WiFi or cellular connection.'
      )
    }

    if (err instanceof Error) {
      const errorMessage = err.message.toLowerCase()
      
      // Extract specific tool failure information if available
      let toolContext = ''
      if (failedComponent) {
        toolContext = ` The analysis failed during the "${failedComponent}" step.`
      }
      
      // Network/connection errors
      if (errorMessage.includes('fetch') || 
          errorMessage.includes('network') || 
          errorMessage.includes('connection') ||
          errorMessage.includes('timeout') ||
          errorMessage.includes('abort')) {
        return createErrorState(
          `Connection problem detected.${toolContext} This might be due to internet connectivity issues.`,
          'connection',
          true,
          'The connection to the server was interrupted. This often happens with unstable internet connections or when processing complex transactions.'
        )
      }
      
      // Server errors
      if (errorMessage.includes('server') || 
          errorMessage.includes('internal') ||
          errorMessage.includes('service unavailable') ||
          errorMessage.includes('502') ||
          errorMessage.includes('503') ||
          errorMessage.includes('504')) {
        return createErrorState(
          `Server is experiencing issues.${toolContext} Please try again in a moment.`,
          'server',
          true,
          'The analysis server is temporarily unavailable or overloaded. This is usually temporary.'
        )
      }
      
      // Timeout errors
      if (errorMessage.includes('timeout') || errorMessage.includes('context cancel')) {
        return createErrorState(
          `Request timed out.${toolContext} This transaction might be complex or the server is busy.`,
          'timeout',
          true,
          'The analysis took longer than expected. Complex transactions may require more time. Try again as the issue might be temporary.'
        )
      }
      
      // LLM-specific errors
      if (errorMessage.includes('llm call failed') || errorMessage.includes('ai analysis')) {
        return createErrorState(
          `AI analysis failed.${toolContext} This might be due to high server load.`,
          'server',
          true,
          'The AI analysis component encountered an issue. This is often temporary due to high demand on AI services.'
        )
      }
      
      // Generic error with tool context
      return createErrorState(
        `${err.message}${toolContext}`,
        'unknown',
        true,
        failedComponent ? `The error occurred during the ${failedComponent} analysis step.` : 'An unexpected error occurred during analysis.'
      )
    }
    
    return createErrorState(
      'An unexpected error occurred',
      'unknown',
      true
    )
  }

  const fetchNetworks = async () => {
    try {
      const response = await fetch('/api/v1/networks')
      if (!response.ok) throw new Error('Failed to fetch networks')
      const data = await response.json()
      setNetworks(data.networks)
    } catch (err) {
      console.error('Failed to fetch networks:', err)
      const errorState = handleError(err, 'Network loading')
      // For network loading, show a simpler error
      setError(createErrorState(
        'Failed to load supported networks. Please refresh the page.',
        errorState.type,
        true
      ))
    }
  }

  const explainTransaction = async (request: TransactionRequest, isRetry: boolean = false) => {
    // Store the request for potential retry
    if (!isRetry) {
      setLastRequest(request)
      setRetryCount(0)
    }

    setLoading(true)
    setError(null)
    setResult(null)
    setComponents([]) // Start with empty components, let backend drive all updates

    // Scroll to the progress display area after a brief delay to ensure it's rendered
    setTimeout(() => {
      if (progressDisplayRef.current) {
        progressDisplayRef.current.scrollIntoView({
          behavior: 'smooth',
          block: 'start',
          inline: 'nearest'
        })
      }
    }, 100)

    // Check network connectivity first
    if (!navigator.onLine) {
      setError(createErrorState(
        'No internet connection. Please check your network and try again.',
        'network',
        true,
        'Your device is offline. Connect to the internet to analyze transactions.'
      ))
      setLoading(false)
      return
    }

    let timeoutId: NodeJS.Timeout | null = null
    let abortController: AbortController | null = null
    let lastFailedComponent: string | undefined

    try {
      abortController = new AbortController()
      
      // Adaptive timeout based on retry count
      const baseTimeout = 5 * 60 * 1000 // 5 minutes base
      const timeoutMultiplier = Math.min(1 + (retryCount * 0.5), 2) // Up to 2x timeout on retries
      const adaptiveTimeout = baseTimeout * timeoutMultiplier
      
      timeoutId = setTimeout(() => {
        if (abortController) {
          abortController.abort()
        }
      }, adaptiveTimeout)

      // Use fetch with SSE streaming instead of EventSource (which doesn't support POST)
      const response = await fetch('/api/v1/explain-sse', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(request),
        signal: abortController.signal
      })

      if (!response.ok) {
        if (response.status >= 500) {
          throw new Error(`Server error (${response.status}): The analysis server is experiencing issues`)
        } else if (response.status === 429) {
          throw new Error('Too many requests. Please wait a moment and try again.')
        } else if (response.status >= 400) {
          throw new Error(`Request error (${response.status}): Please check your transaction hash and network selection`)
        }
        throw new Error(`HTTP error ${response.status}`)
      }

      const reader = response.body?.getReader()
      const decoder = new TextDecoder()

      if (!reader) {
        throw new Error('No response stream available')
      }

      let buffer = ''
      let eventCount = 0
      let firstEventTime: number | null = null
      let lastEventTime = Date.now()

      // Set up a heartbeat timeout to detect stalled connections
      const heartbeatTimeout = setInterval(() => {
        const now = Date.now()
        if (now - lastEventTime > 30000) { // 30 seconds without data
          console.warn('[SSE-CLIENT] Connection appears stalled, aborting...')
          if (abortController) {
            abortController.abort()
          }
        }
      }, 10000) // Check every 10 seconds

      try {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          const readTime = Date.now()
          lastEventTime = readTime
          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          
          // Keep the last incomplete line in buffer
          buffer = lines.pop() || ''

          for (const line of lines) {
            // Log all SSE lines for debugging
            if (line.trim()) {
              console.log(`[SSE-CLIENT-DEBUG] Line received at ${readTime}: ${line.substring(0, 100)}...`)
            }
            
            if (line.startsWith('data: ')) {
              try {
                eventCount++
                const parseTime = Date.now()
                if (!firstEventTime) firstEventTime = parseTime
                
                const eventData = JSON.parse(line.slice(6)) as ProgressEvent
                console.log(`[SSE-CLIENT-DEBUG] Event ${eventCount} parsed at ${parseTime} (${parseTime - readTime}ms after read, ${parseTime - firstEventTime}ms since first event):`, eventData.type, eventData.component?.id)
                
                if (eventData.type === 'component_update' && eventData.component) {
                  const updateTime = Date.now()
                  
                  // Track the last component that had an error for better error reporting
                  if (eventData.component.status === 'error') {
                    lastFailedComponent = eventData.component.title
                  }
                  
                  setComponents(prev => {
                    const existing = prev.find(c => c.id === eventData.component!.id)
                    if (existing) {
                      return prev.map(c => c.id === eventData.component!.id ? eventData.component! : c)
                    } else {
                      return [...prev, eventData.component!]
                    }
                  })
                  console.log(`[SSE-CLIENT-DEBUG] Component updated at ${updateTime} (${updateTime - parseTime}ms after parse)`)
                } else if (eventData.type === 'complete' && eventData.result) {
                  console.log('[SSE-CLIENT-DEBUG] Analysis completed, total components received:', components.length)
                  setResult(eventData.result as ExplanationResult)
                  setLoading(false)
                  clearInterval(heartbeatTimeout)
                  if (timeoutId) clearTimeout(timeoutId)
                  return
                } else if (eventData.type === 'error') {
                  console.log('[SSE-CLIENT-DEBUG] Server sent error:', eventData.error, 'Components received:', components.length)
                  
                  // Handle server-sent errors with proper categorization
                  const serverError = eventData.error || 'Analysis failed'
                  let errorType: ErrorState['type'] = 'server'
                  
                  if (serverError.toLowerCase().includes('context canceled') || 
                      serverError.toLowerCase().includes('timeout')) {
                    errorType = 'timeout'
                  } else if (serverError.toLowerCase().includes('network') || 
                             serverError.toLowerCase().includes('connection')) {
                    errorType = 'connection'
                  }
                  
                  setError(createErrorState(
                    `Analysis failed: ${serverError}`,
                    errorType,
                    true,
                    lastFailedComponent 
                      ? `The server encountered an error during the ${lastFailedComponent} step. This may be due to server load or a complex transaction.`
                      : 'The server encountered an error while analyzing your transaction. This may be due to server load or a complex transaction.'
                  ))
                  setLoading(false)
                  clearInterval(heartbeatTimeout)
                  if (timeoutId) clearTimeout(timeoutId)
                  return
                }
              } catch (e) {
                console.error('[SSE-CLIENT-DEBUG] Failed to parse SSE data:', e, 'Line:', line)
              }
            }
          }
        }
      } finally {
        clearInterval(heartbeatTimeout)
      }
    } catch (err) {
      // Clear timeouts
      if (timeoutId) clearTimeout(timeoutId)
      
      // Handle different error types
      if (err instanceof Error && err.name === 'AbortError') {
        setError(createErrorState(
          'Analysis was cancelled or timed out. Complex transactions may take longer to analyze.',
          'timeout',
          true,
          lastFailedComponent 
            ? `The request was cancelled during the ${lastFailedComponent} step, either manually or due to a timeout. Try again with a longer timeout.`
            : 'The request was cancelled, either manually or due to a timeout. Try again with a shorter transaction hash or check your internet connection.'
        ))
      } else {
        setError(handleError(err, 'Transaction analysis', lastFailedComponent))
      }
    } finally {
      setLoading(false)
    }
  }

  const retryAnalysis = () => {
    if (error && error.isRetryable && lastRequest) {
      setRetryCount(prev => prev + 1)
      setError(null)
      explainTransaction(lastRequest, true) // Pass true to indicate this is a retry
    }
  }

  const clearError = () => {
    setError(null)
    setRetryCount(0) // Reset retry count when manually clearing
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
              {/* Network Status Indicator */}
              {!isOnline && (
                <div className="flex items-center space-x-2 px-3 py-1 bg-red-100 text-red-800 text-sm rounded-full">
                  <div className="w-2 h-2 bg-red-500 rounded-full"></div>
                  <span>Offline</span>
                </div>
              )}
              
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

          {/* Error Display - shown in place of Analysis section */}
          {error && (
            <div ref={progressDisplayRef} className="mb-6">
              <div className={`bg-white rounded-lg shadow-lg p-6 border-l-4 ${
                error.type === 'network' || error.type === 'connection' 
                  ? 'border-red-400' 
                  : error.type === 'timeout'
                  ? 'border-yellow-400'
                  : error.type === 'server'
                  ? 'border-orange-400'
                  : 'border-gray-400'
              }`}>
                <div className="flex items-start">
                  <div className="flex-shrink-0">
                    {error.type === 'network' || error.type === 'connection' ? (
                      <svg className="w-5 h-5 text-red-400 mt-0.5" fill="currentColor" viewBox="0 0 20 20">
                        <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clipRule="evenodd" />
                      </svg>
                    ) : error.type === 'timeout' ? (
                      <svg className="w-5 h-5 text-yellow-400 mt-0.5" fill="currentColor" viewBox="0 0 20 20">
                        <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm1-12a1 1 0 10-2 0v4a1 1 0 00.293.707l2.828 2.829a1 1 0 101.415-1.415L11 9.586V6z" clipRule="evenodd" />
                      </svg>
                    ) : (
                      <svg className="w-5 h-5 text-orange-400 mt-0.5" fill="currentColor" viewBox="0 0 20 20">
                        <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a1 1 0 000 2v3a1 1 0 001 1h1a1 1 0 100-2v-3a1 1 0 00-1-1H9z" clipRule="evenodd" />
                      </svg>
                    )}
                  </div>
                  <div className="ml-3 flex-1">
                    <h3 className="text-sm font-medium text-gray-900">
                      {error.type === 'network' || error.type === 'connection' 
                        ? 'Connection Problem'
                        : error.type === 'timeout'
                        ? 'Request Timed Out'
                        : error.type === 'server'
                        ? 'Server Error'
                        : 'Error'
                      }
                    </h3>
                    <div className="mt-1 text-sm text-gray-600">
                      <p>{error.message}</p>
                      {error.details && (
                        <p className="mt-2 text-xs opacity-75">{error.details}</p>
                      )}
                    </div>
                    {error.isRetryable && (
                      <div className="mt-3 flex items-center space-x-3">
                        <button
                          onClick={retryAnalysis}
                          disabled={loading}
                          className={`text-sm px-3 py-1 rounded transition-colors flex items-center space-x-1 ${
                            loading 
                              ? 'bg-gray-400 text-white cursor-not-allowed' 
                              : 'bg-blue-600 text-white hover:bg-blue-700'
                          }`}
                        >
                          {loading ? (
                            <>
                              <div className="w-3 h-3 border-2 border-white border-t-transparent rounded-full animate-spin"></div>
                              <span>Retrying...</span>
                            </>
                          ) : (
                            <>
                              <span>Try Again</span>
                              {retryCount > 0 && (
                                <span className="ml-1 text-xs opacity-75">({retryCount} attempts)</span>
                              )}
                            </>
                          )}
                        </button>
                        <button
                          onClick={clearError}
                          disabled={loading}
                          className={`text-sm transition-colors ${
                            loading 
                              ? 'text-gray-400 cursor-not-allowed' 
                              : 'text-gray-500 hover:text-gray-700'
                          }`}
                        >
                          Dismiss
                        </button>
                        {retryCount > 0 && (
                          <span className="text-xs text-gray-500">
                            Previous attempts: {retryCount}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </div>
          )}

          {/* Progress Display - only shown when no error */}
          {!error && (loading || components.length > 0 || result !== null) && (
            <div ref={progressDisplayRef}>
              <ProgressDisplay 
                components={components} 
                isComplete={!loading && result !== null}
              />
            </div>
          )}

          {result && <ResultsDisplay result={result} />}
        </div>
      </div>
    </div>
  )
}

export default App
