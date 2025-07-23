import React, { useState, useEffect } from 'react'
import type { ComponentUpdate, ComponentGroup } from '../types'

interface ProgressDisplayProps {
  components: ComponentUpdate[]
  isComplete: boolean
}

const ProgressDisplay: React.FC<ProgressDisplayProps> = ({ components, isComplete }) => {
  const [isExpanded, setIsExpanded] = useState(true)
  const [liveTimers, setLiveTimers] = useState<Record<string, number>>({})

  // Auto-collapse when analysis is complete
  useEffect(() => {
    if (isComplete) {
      setIsExpanded(false)
    }
  }, [isComplete])

  // Live timer effect for running components
  useEffect(() => {
    const interval = setInterval(() => {
      const now = Date.now()
      const newTimers: Record<string, number> = {}
      
      components.forEach(component => {
        if (component.status === 'running' || component.status === 'initiated') {
          const startTime = component.start_time ? new Date(component.start_time).getTime() : now
          const elapsed = Math.max(0, now - startTime)
          newTimers[component.id] = elapsed
        }
      })
      
      setLiveTimers(newTimers)
    }, 100) // Update every 100ms for smooth timer animation

    return () => clearInterval(interval)
  }, [components])

  // Group components by their group
  const groupedComponents = components.reduce((acc, component) => {
    if (!acc[component.group]) {
      acc[component.group] = []
    }
    acc[component.group].push(component)
    return acc
  }, {} as Record<ComponentGroup, ComponentUpdate[]>)

  // Define group colors and labels
  const groupConfig = {
    data: { 
      label: 'Data Collection', 
      color: 'bg-blue-50 border-blue-200 text-blue-800',
      accentColor: 'bg-blue-500'
    },
    decoding: { 
      label: 'Decoding & Parsing', 
      color: 'bg-green-50 border-green-200 text-green-800',
      accentColor: 'bg-green-500'
    },
    enrichment: { 
      label: 'Data Enrichment', 
      color: 'bg-purple-50 border-purple-200 text-purple-800',
      accentColor: 'bg-purple-500'
    },
    analysis: { 
      label: 'AI Analysis', 
      color: 'bg-orange-50 border-orange-200 text-orange-800',
      accentColor: 'bg-orange-500'
    },
    finishing: { 
      label: 'Finalizing', 
      color: 'bg-gray-50 border-gray-200 text-gray-800',
      accentColor: 'bg-gray-500'
    }
  }

  // Status configurations
  const statusConfig = {
    initiated: { 
      icon: '⏳', 
      iconColor: 'text-gray-400',
      bgColor: 'bg-gray-100',
      animate: false,
      showSpinner: false
    },
    running: { 
      icon: '⚡', 
      iconColor: 'text-blue-500',
      bgColor: 'bg-blue-100',
      animate: true,
      showSpinner: true
    },
    finished: { 
      icon: '✅', 
      iconColor: 'text-green-500',
      bgColor: 'bg-green-100',
      animate: false,
      showSpinner: false
    },
    error: { 
      icon: '❌', 
      iconColor: 'text-red-500',
      bgColor: 'bg-red-100',
      animate: false,
      showSpinner: false
    }
  }

  if (components.length === 0) {
    return null
  }

  // Calculate summary statistics
  const completedComponents = components.filter(c => c.status === 'finished').length
  const errorComponents = components.filter(c => c.status === 'error').length
  const firstComponent = components.length > 0 ? components[0] : null
  const lastComponent = components.length > 0 ? components[components.length - 1] : null
  
  // Calculate total duration if analysis is complete
  const totalDuration = firstComponent && lastComponent && isComplete
    ? new Date(lastComponent.timestamp).getTime() - new Date(firstComponent.timestamp).getTime()
    : 0

  const formatDuration = (ms: number) => {
    if (ms < 1000) return `${ms}ms`
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
    return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
  }

  // Helper function to format component duration with live timer for running components
  const formatComponentDuration = (component: ComponentUpdate) => {
    // For running components, use live timer if available
    if (component.status === 'running' || component.status === 'initiated') {
      const liveElapsed = liveTimers[component.id]
      if (liveElapsed !== undefined) {
        if (liveElapsed < 1000) {
          return `${Math.floor(liveElapsed)}ms`
        } else {
          return `${(liveElapsed / 1000).toFixed(1)}s`
        }
      }
      // Fallback to "Starting..." if no live timer yet
      return 'Starting...'
    }
    
    // For finished/error components, use server-calculated duration
    if (component.duration_ms === 0) {
      return 'Starting...'
    } else if (component.duration_ms < 1000) {
      return `${component.duration_ms}ms`
    } else {
      return `${(component.duration_ms / 1000).toFixed(1)}s`
    }
  }

  return (
    <div className="bg-white rounded-lg shadow-lg mb-6">
      {/* Header - always visible */}
      <div 
        className="flex items-center justify-between p-4 cursor-pointer hover:bg-gray-50 transition-colors"
        onClick={() => setIsExpanded(!isExpanded)}
      >
        <div className="flex items-center space-x-3">
          <h3 className="text-lg font-semibold text-gray-900">Analysis Progress</h3>
          {isComplete && (
            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-800">
              <span className="mr-1">✅</span>
              Completed
            </span>
          )}
        </div>
        <div className="flex items-center space-x-3">
          {isComplete && !isExpanded && (
            <div className="text-sm text-gray-600">
              {completedComponents} steps • {formatDuration(totalDuration)}
              {errorComponents > 0 && (
                <span className="text-red-600 ml-2">{errorComponents} errors</span>
              )}
            </div>
          )}
          <button className="p-1 hover:bg-gray-200 rounded">
            <svg 
              className={`w-5 h-5 text-gray-500 transition-transform ${isExpanded ? 'rotate-180' : ''}`}
              fill="none" 
              stroke="currentColor" 
              viewBox="0 0 24 24"
            >
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
            </svg>
          </button>
        </div>
      </div>

      {/* Detailed progress - collapsible */}
      {isExpanded && (
        <div className="px-4 pb-4 border-t border-gray-100">

      <div className="space-y-4">
        {Object.entries(groupedComponents).map(([group, groupComponents]) => {
          const config = groupConfig[group as ComponentGroup]
          if (!config) return null

          return (
            <div key={group} className={`rounded-lg border p-4 ${config.color}`}>
              <div className="flex items-center mb-3">
                <div className={`w-3 h-3 rounded-full mr-2 ${config.accentColor}`}></div>
                <h4 className="font-medium text-sm">{config.label}</h4>
              </div>
              
              <div className="space-y-2">
                {/* Separate running and completed components */}
                {(() => {
                  const runningComponents = groupComponents.filter(c => c.status === 'running' || c.status === 'initiated')
                  const completedComponents = groupComponents.filter(c => c.status === 'finished' || c.status === 'error')
                  
                  return (
                    <>
                      {/* Show running/initiated components first */}
                      {runningComponents
                        .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
                        .map((component) => {
                          const status = statusConfig[component.status]
                          
                          return (
                            <div key={component.id} className="flex items-center justify-between p-2 bg-white bg-opacity-50 rounded border-l-2 border-blue-400">
                              <div className="flex items-center space-x-3 flex-1 min-w-0">
                                <div className={`flex-shrink-0 w-6 h-6 rounded-full flex items-center justify-center text-sm ${status.bgColor}`}>
                                  {status.showSpinner ? (
                                    <div className="w-4 h-4 border-2 border-blue-500 border-t-transparent rounded-full animate-spin"></div>
                                  ) : (
                                    <span className={`${status.iconColor} ${status.animate ? 'animate-pulse' : ''}`}>
                                      {status.icon}
                                    </span>
                                  )}
                                </div>
                                <div className="flex-1 min-w-0">
                                  <p className="text-sm font-medium text-gray-900 truncate">
                                    {component.title}
                                  </p>
                                  {component.description && (
                                    <p className="text-xs text-gray-600 truncate">
                                      {component.description}
                                    </p>
                                  )}
                                </div>
                              </div>
                              <div className="flex-shrink-0 text-xs text-gray-500">
                                {formatComponentDuration(component)}
                              </div>
                            </div>
                          )
                        })}
                      
                      {/* Show completed components in compact format */}
                      {completedComponents.length > 0 && (
                        <div className="mt-3 pt-2 border-t border-gray-200">
                          <div className="space-y-1">
                            {completedComponents
                              .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
                              .map((component) => {
                                const status = statusConfig[component.status]
                                
                                return (
                                  <div key={component.id} className="flex items-center justify-between py-1">
                                    <div className="flex items-center space-x-2 flex-1 min-w-0">
                                      <div className={`flex-shrink-0 w-4 h-4 rounded-full flex items-center justify-center text-xs ${status.bgColor}`}>
                                        <span className={status.iconColor}>
                                          {status.icon}
                                        </span>
                                      </div>
                                      <div className="flex-1 min-w-0">
                                        <p className="text-xs font-medium text-gray-700 truncate">
                                          {component.title}
                                        </p>
                                      </div>
                                    </div>
                                    <div className="flex-shrink-0 text-xs text-gray-400">
                                      {formatComponentDuration(component)}
                                    </div>
                                  </div>
                                )
                              })}
                          </div>
                        </div>
                      )}
                    </>
                  )
                })()}
              </div>
            </div>
          )
        })}
        </div>
        </div>
      )}
    </div>
  )
}

export default ProgressDisplay 