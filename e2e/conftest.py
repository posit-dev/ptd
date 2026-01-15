import pytest
from pathlib import Path
from playwright.sync_api import Page


@pytest.fixture(scope="session", autouse=True)
def capture_site_screenshot(browser, base_url):
    """Capture a screenshot of the site homepage at the start of the test session."""
    output_dir = Path("test-results")
    output_dir.mkdir(exist_ok=True)

    # Create a new page for the screenshot
    page = browser.new_page()
    try:
        page.goto(base_url)
        page.wait_for_load_state("networkidle")

        # Capture full page screenshot
        screenshot_path = output_dir / "site-overview.png"
        page.screenshot(path=str(screenshot_path), full_page=True)
        print(f"\nSite screenshot saved to: {screenshot_path}")
    finally:
        page.close()
