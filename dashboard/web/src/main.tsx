import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './tokens.css'
import './index.css'
import App from './App.tsx'
import JoinPage from './components/JoinPage.tsx'
import PlaygroundPage from './components/PlaygroundPage.tsx'

// The attendee /join page and the booth-ops /playground page are separate,
// self-contained trees: neither needs the fabric map's WebSocket/store, so
// they're switched on pathname here (outside App) rather than as an early
// return inside App — that keeps App's hooks (including the connectLive
// effect) from ever mounting on these routes.
const page = location.pathname.startsWith('/join')
  ? <JoinPage />
  : location.pathname.startsWith('/playground')
  ? <PlaygroundPage />
  : <App />

// The map app owns a fixed, non-scrolling viewport (body { overflow: hidden }
// in index.css). The /join and /playground routes are ordinary documents that
// must scroll their content, so tag the body for the CSS override below.
if (location.pathname.startsWith('/join') || location.pathname.startsWith('/playground')) {
  document.body.classList.add('doc-page')
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {page}
  </StrictMode>,
)
