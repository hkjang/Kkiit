import { createTheme } from '@mui/material/styles'

export const theme = createTheme({
  palette: {
    mode: 'light',
    primary: { main: '#356df3', dark: '#1746b7', light: '#eaf0ff' },
    secondary: { main: '#0aa181', dark: '#05705b', light: '#e4f8f2' },
    background: { default: '#f5f7fb', paper: '#ffffff' },
    text: { primary: '#12233f', secondary: '#5e6b7f' },
    divider: '#e3e8f1',
    error: { main: '#c94153' },
    warning: { main: '#d97706' },
    success: { main: '#16866b' },
  },
  typography: {
    fontFamily: 'Inter, Pretendard, "Noto Sans KR", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    fontSize: 16,
    h1: { fontSize: 'clamp(2.2rem, 5vw, 4.4rem)', lineHeight: 1.08, fontWeight: 800, letterSpacing: '-0.045em' },
    h2: { fontSize: '2rem', lineHeight: 1.22, fontWeight: 750, letterSpacing: '-0.03em' },
    h3: { fontSize: '1.4rem', lineHeight: 1.3, fontWeight: 720, letterSpacing: '-0.02em' },
    h4: { fontSize: '1.16rem', lineHeight: 1.4, fontWeight: 700 },
    body1: { fontSize: '1rem', lineHeight: 1.65 },
    body2: { fontSize: '0.94rem', lineHeight: 1.58 },
    button: { fontSize: '0.95rem', fontWeight: 700, textTransform: 'none' },
  },
  shape: { borderRadius: 14 },
  components: {
    MuiButton: { defaultProps: { disableElevation: true }, styleOverrides: { root: { minHeight: 44, borderRadius: 11, paddingInline: 18 } } },
    MuiTextField: { defaultProps: { size: 'medium' } },
    MuiCard: { styleOverrides: { root: { border: '1px solid #e3e8f1', boxShadow: '0 10px 32px rgba(28, 51, 84, .06)' } } },
    MuiMenuItem: { styleOverrides: { root: { minHeight: 44 } } },
    MuiChip: { styleOverrides: { root: { fontWeight: 650 } } },
  },
})
