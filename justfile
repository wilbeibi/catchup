# list available recipes
default:
    @just --list

# show site branch status
status:
    git status --short --branch

# create the static asset directory expected by Cloudflare Pages
init:
    mkdir -p public

# deploy the site branch to Cloudflare Pages
deploy:
    npx wrangler pages deploy public --project-name=catchup --branch=site

# print the reserved Pages URL
url:
    @echo "https://catchup.pages.dev/"
