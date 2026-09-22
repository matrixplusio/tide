import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { initI18n } from './lib/i18n'
import './styles/tokens.css'
import './styles/base.css'
import App from './app/App'
import { ME_KEY } from './app/session'
import { ApiError } from './lib/api'
import { ErrCode } from './lib/errcode'
import { ToastProvider } from './components/ui'

// Before the first render: components read translations as they mount.
initI18n()

// A lost session (1002) or an uninitialised install (2003) anywhere sends the
// app back through the /me check, which renders Login or redirects to /setup.
function onGlobalError(err: unknown, key?: readonly unknown[]) {
  if (!(err instanceof ApiError)) return
  if (err.code !== ErrCode.Unauthenticated && err.code !== ErrCode.SetupRequired) return
  if (key && key[0] === ME_KEY[0]) return
  void qc.resetQueries({ queryKey: ME_KEY })
}

const qc: QueryClient = new QueryClient({
  queryCache: new QueryCache({ onError: (err, query) => onGlobalError(err, query.queryKey) }),
  mutationCache: new MutationCache({ onError: (err) => onGlobalError(err) }),
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchOnWindowFocus: true,
      retry: (n, err) => !(err instanceof ApiError && err.httpStatus > 0 && err.httpStatus < 500) && n < 2,
    },
  },
})

const root = document.getElementById('root')
if (!root) throw new Error('#root missing')

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
)
