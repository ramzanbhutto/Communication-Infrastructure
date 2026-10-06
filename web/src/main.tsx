import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './app';
import './styles.css';
import { setNonce } from 'get-nonce';

const nonce = document.querySelector('meta[name="csp-nonce"]')?.getAttribute('content');
if (nonce && nonce !== '__OPS_NONCE__') setNonce(nonce);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
