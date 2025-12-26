import os
from playwright.sync_api import sync_playwright

def run(playwright):
    browser = playwright.chromium.launch()
    context = browser.new_context()
    page = browser.new_page()

    base_path = os.getcwd()

    # Verify index.html
    page.goto(f"file://{base_path}/webpages/index.html")
    page.screenshot(path="webpages/index.png")

    # Verify terminal.html
    page.goto(f"file://{base_path}/webpages/terminal.html")
    page.screenshot(path="webpages/terminal.png")

    # Verify classic.html
    page.goto(f"file://{base_path}/webpages/classic.html")
    page.screenshot(path="webpages/classic.png")

    # Verify dark_mode.html
    page.goto(f"file://{base_path}/webpages/dark_mode.html")
    page.screenshot(path="webpages/dark_mode.png")

    # Verify light_mode.html
    page.goto(f"file://{base_path}/webpages/light_mode.html")
    page.screenshot(path="webpages/light_mode.png")

    # Verify retro.html
    page.goto(f"file://{base_path}/webpages/retro.html")
    page.screenshot(path="webpages/retro.png")

    # Verify data_grid.html
    page.goto(f"file://{base_path}/webpages/data_grid.html")
    page.screenshot(path="webpages/data_grid.png")

    # Verify cards.html
    page.goto(f"file://{base_path}/webpages/cards.html")
    page.screenshot(path="webpages/cards.png")

    # Verify minimalist.html
    page.goto(f"file://{base_path}/webpages/minimalist.html")
    page.screenshot(path="webpages/minimalist.png")

    # Verify corporate.html
    page.goto(f"file://{base_path}/webpages/corporate.html")
    page.screenshot(path="webpages/corporate.png")

    browser.close()

with sync_playwright() as playwright:
    run(playwright)
