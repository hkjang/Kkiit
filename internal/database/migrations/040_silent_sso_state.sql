-- A prompt=none sign in never shows a screen: the provider either answers with
-- a code or with login_required. The callback has to know which kind of request
-- it is answering to send a refused silent attempt back to the login page, and
-- where the person was going so a deep link lands them there afterwards.
ALTER TABLE oauth_states ADD COLUMN IF NOT EXISTS silent boolean NOT NULL DEFAULT false;
ALTER TABLE oauth_states ADD COLUMN IF NOT EXISTS return_to text NOT NULL DEFAULT '/';
