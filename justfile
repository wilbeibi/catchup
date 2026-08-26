# list available recipes
default:
    @just --list

# show site branch status
status:
    git status --short --branch

# create the static asset directory expected by Cloudflare Pages
init:
    mkdir -p public

# serve the static site locally
serve port="8788":
    cd public && python3 -m http.server {{port}}

# serve on this machine's tailscale IP so other tailnet devices can preview
preview port="8788":
    @ip=$(tailscale ip -4 | head -n1) && echo "Preview at http://$ip:{{port}}/ (tailnet-only, ctrl-c to stop)" && cd public && python3 -m http.server {{port}} --bind "$ip"

# deploy the site branch to Cloudflare Pages
deploy: publish-installer
    npx wrangler pages deploy public --project-name=catchup --branch=site

# copy the installer from main into public/ (CI does this on every deploy)
publish-installer:
    ./scripts/publish-installer.sh

# print the reserved Pages URL
url:
    @echo "https://catchup.pages.dev/"
