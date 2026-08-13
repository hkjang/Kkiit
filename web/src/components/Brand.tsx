import { Box, Typography } from '@mui/material'
import AutoAwesomeRoundedIcon from '@mui/icons-material/AutoAwesomeRounded'

export function Brand({ inverse = false }: { inverse?: boolean }) {
  return <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 1.15 }}>
    <Box sx={{ width: 36, height: 36, borderRadius: '11px', display: 'grid', placeItems: 'center', color: inverse ? '#0b203a' : 'white', bgcolor: inverse ? '#a9f3df' : 'primary.main', boxShadow: inverse ? '0 6px 18px rgba(10,161,129,.25)' : '0 6px 18px rgba(53,109,243,.24)' }}><AutoAwesomeRoundedIcon fontSize="small" /></Box>
    <Typography component="span" sx={{ fontSize: '1.42rem', lineHeight: 1, fontWeight: 850, letterSpacing: '-.04em', color: inverse ? 'white' : 'text.primary' }}>Kkiit</Typography>
  </Box>
}
