import React from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { CssBaseline, ThemeProvider, createTheme } from '@mui/material'
import App from './App'

const theme = createTheme({
  palette: { primary: { main: '#1d66b4' }, background: { default: '#f7f8fa', paper: '#fff' }, text: { primary: '#182333', secondary: '#647081' } },
  typography: { fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans SC", sans-serif', h4: { letterSpacing: '-0.035em' } },
  shape: { borderRadius: 10 }
})

createRoot(document.getElementById('root')!).render(<React.StrictMode><ThemeProvider theme={theme}><CssBaseline /><BrowserRouter><App /></BrowserRouter></ThemeProvider></React.StrictMode>)
