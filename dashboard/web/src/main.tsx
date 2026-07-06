import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './tokens.css'
import './index.css'
import App from './App.tsx'
import JoinPage from './components/JoinPage.tsx'

// The attendee /join page is a separate, self-contained tree: it has no need
// for the fabric map's WebSocket/store, so it's switched on pathname here
// (outside App) rather than as an early return inside App — that keeps App's
// hooks (including the connectLive effect) from ever mounting on this route.
const page = location.pathname.startsWith('/join') ? <JoinPage /> : <App />

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {page}
  </StrictMode>,
)
