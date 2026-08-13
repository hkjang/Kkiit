import { useState } from 'react'
import { Outlet, Link as RouterLink, useNavigate } from 'react-router-dom'
import { AppBar, Avatar, Box, Button, Container, Divider, IconButton, ListItemIcon, Menu, MenuItem, Stack, Toolbar, Tooltip, Typography } from '@mui/material'
import SearchRoundedIcon from '@mui/icons-material/SearchRounded'
import ReceiptLongRoundedIcon from '@mui/icons-material/ReceiptLongRounded'
import AdminPanelSettingsRoundedIcon from '@mui/icons-material/AdminPanelSettingsRounded'
import PersonOutlineRoundedIcon from '@mui/icons-material/PersonOutlineRounded'
import KeyRoundedIcon from '@mui/icons-material/KeyRounded'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import LogoutRoundedIcon from '@mui/icons-material/LogoutRounded'
import LoginRoundedIcon from '@mui/icons-material/LoginRounded'
import { Brand } from './Brand'
import { api } from '../api'
import { useApp } from '../App'

export function AppShell() {
  const { me, version, refreshMe, notify } = useApp()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const navigate = useNavigate()
  const logout = async () => {
    try { await api('/api/v1/auth/logout', { method: 'POST' }); await refreshMe(); navigate('/login') }
    catch (error) { notify(error instanceof Error ? error.message : '로그아웃하지 못했습니다.', 'error') }
  }
  return <Box sx={{ minHeight: '100vh' }}>
    <AppBar position="sticky" color="inherit" elevation={0} sx={{ borderBottom: '1px solid', borderColor: 'divider', bgcolor: 'rgba(255,255,255,.92)', backdropFilter: 'blur(18px)' }}>
      <Container maxWidth="xl"><Toolbar disableGutters sx={{ minHeight: 72, gap: 1 }}>
        <Box component={RouterLink} to="/" aria-label="Kkiit 홈" sx={{ textDecoration: 'none', mr: { xs: 1, md: 4 } }}><Brand /></Box>
        <Button component={RouterLink} to="/" color="inherit" startIcon={<SearchRoundedIcon />} sx={{ display: { xs: 'none', sm: 'inline-flex' } }}>서비스 찾기</Button>
        {me && <Button component={RouterLink} to="/orders" color="inherit" startIcon={<ReceiptLongRoundedIcon />} sx={{ display: { xs: 'none', sm: 'inline-flex' } }}>내 주문</Button>}
        <Box sx={{ flex: 1 }} />
        {me ? <>
          {me.permissions.includes('admin.access') && <Tooltip title="서비스 관리자"><IconButton component={RouterLink} to="/admin" color="primary" aria-label="서비스 관리자 열기"><AdminPanelSettingsRoundedIcon /></IconButton></Tooltip>}
          <Button color="inherit" onClick={(event) => setAnchor(event.currentTarget)} aria-controls={anchor ? 'profile-menu' : undefined} aria-haspopup="true" aria-expanded={anchor ? 'true' : undefined} sx={{ ml: 1, px: 1 }}>
            <Avatar sx={{ width: 36, height: 36, bgcolor: 'secondary.light', color: 'secondary.dark', fontWeight: 800 }}>{me.display_name.slice(0, 1)}</Avatar>
            <Typography sx={{ ml: 1.2, display: { xs: 'none', md: 'block' }, fontWeight: 700 }}>{me.display_name}</Typography>
          </Button>
          <Menu id="profile-menu" anchorEl={anchor} open={Boolean(anchor)} onClose={() => setAnchor(null)} slotProps={{ paper: { sx: { width: 260, mt: 1, p: .75 } } }}>
            <Box sx={{ px: 1.5, py: 1 }}><Typography fontWeight={750}>{me.display_name}</Typography><Typography variant="body2" color="text.secondary">{me.email ?? `@${me.username}`}</Typography></Box>
            <Divider sx={{ my: .5 }} />
            <MenuItem component={RouterLink} to="/profile" onClick={() => setAnchor(null)}><ListItemIcon><PersonOutlineRoundedIcon fontSize="small" /></ListItemIcon>개인화 페이지</MenuItem>
            <MenuItem component={RouterLink} to="/profile/keys" onClick={() => setAnchor(null)}><ListItemIcon><KeyRoundedIcon fontSize="small" /></ListItemIcon>API 키 관리</MenuItem>
            <Divider sx={{ my: .5 }} />
            <MenuItem disabled sx={{ opacity: '1 !important' }}><ListItemIcon><InfoOutlinedIcon fontSize="small" /></ListItemIcon><Stack><span>서비스 버전</span><Typography variant="caption" color="text.secondary">v{version?.version ?? '확인 중'} · API {version?.api_version ?? 'v1'}</Typography></Stack></MenuItem>
            <MenuItem onClick={logout}><ListItemIcon><LogoutRoundedIcon fontSize="small" /></ListItemIcon>로그아웃</MenuItem>
          </Menu>
        </> : <Button component={RouterLink} to="/login" variant="contained" startIcon={<LoginRoundedIcon />}>로그인</Button>}
      </Toolbar></Container>
    </AppBar>
    <Outlet />
  </Box>
}
