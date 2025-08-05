import os
import sqlite3
import shutil
import platform
from dotenv import load_dotenv, set_key, find_dotenv


def find_firefox_profile_path():
    """
    Automatically finds the Firefox profile path on Windows.
    """
    if platform.system() != "Windows":
        print("Automatic profile detection is only supported on Windows.")
        return None

    app_data = os.getenv("APPDATA")
    if not app_data:
        return None
        
    profiles_path = os.path.join(app_data, "Mozilla", "Firefox", "Profiles")
    if not os.path.exists(profiles_path):
        return None

    # Prefer profiles with .default-release in the name
    for profile in os.listdir(profiles_path):
        if ".default-release" in profile:
            profile_dir = os.path.join(profiles_path, profile)
            if os.path.isdir(profile_dir) and "cookies.sqlite" in os.listdir(profile_dir):
                print(f"Found Firefox profile: {profile_dir}")
                return profile_dir

    # Fallback to any profile with a cookie database
    for profile in os.listdir(profiles_path):
        profile_dir = os.path.join(profiles_path, profile)
        if os.path.isdir(profile_dir) and "cookies.sqlite" in os.listdir(profile_dir):
            print(f"Found Firefox profile (fallback): {profile_dir}")
            return profile_dir
            
    return None


def get_firefox_cookies_manual():
    """
    Extracts __Secure-1PSID and __Secure-1PSIDTS cookies from Firefox's SQLite database.
    It will first attempt to find the Firefox profile automatically.
    """
    print("Attempting to extract Gemini cookies from Firefox...")
    load_dotenv()

    profile_path = os.getenv("FIREFOX_PROFILE_PATH")

    if not profile_path:
        print("FIREFOX_PROFILE_PATH not found in .env, attempting to find it automatically...")
        profile_path = find_firefox_profile_path()
        if profile_path:
            dotenv_path = find_dotenv()
            if not dotenv_path:
                dotenv_path = ".env"
                # Create .env file if it doesn't exist
                with open(dotenv_path, "w") as f:
                    f.write(f'FIREFOX_PROFILE_PATH="{profile_path}"\n')
                print(f"Created and saved profile path to {dotenv_path}")
            else:
                set_key(dotenv_path, "FIREFOX_PROFILE_PATH", profile_path)
                print(f"Saved found profile path to {dotenv_path}")
        else:
            print("\n--- CONFIGURATION REQUIRED ---")
            print("Could not automatically find the Firefox profile path.")
            print("Please add the path to your Firefox profile directory to a .env file, for example:")
            print(r'FIREFOX_PROFILE_PATH="C:\Users\YourUsername\AppData\Roaming\Mozilla\Firefox\Profiles\xxxxxxxx.default-release"')
            print("You can find your profile path by navigating to 'about:profiles' in Firefox.")
            print("---------------------------\n")
            return None

    if not os.path.exists(profile_path):
        print(f"\n--- PATH ERROR ---")
        print(f"The specified profile path does not exist: {profile_path}")
        print("Please verify the 'FIREFOX_PROFILE_PATH' in your .env file is correct.")
        print("------------------\n")
        return None

    cookie_db_path = os.path.join(profile_path, "cookies.sqlite")
    if not os.path.exists(cookie_db_path):
        print(f"\n--- PATH ERROR ---")
        print(f"Cookie database not found at: {cookie_db_path}")
        print("Please verify the 'FIREFOX_PROFILE_PATH' in your .env file is correct.")
        print("------------------\n")
        return None

    print(f"Loading cookies from: {cookie_db_path}")

    # To avoid 'database is locked' errors, we must copy the database file.
    temp_db_path = "temp_firefox_cookies.db"
    try:
        shutil.copy2(cookie_db_path, temp_db_path)
    except IOError as e:
        print(f"\n--- FILE ACCESS ERROR ---")
        print(f"Could not copy the cookie database: {e}")
        print("Please ensure Firefox is completely closed and try again.")
        print("-------------------------\n")
        return None

    conn = sqlite3.connect(temp_db_path)
    cursor = conn.cursor()
    cookies = {}

    try:
        # Query the database for google.com cookies
        cursor.execute("SELECT name, value FROM moz_cookies WHERE host LIKE '%.google.com'")
        for name, value in cursor.fetchall():
            if name in ["__Secure-1PSID", "__Secure-1PSIDTS"]:
                if name in cookies: continue # Already found
                
                print(f"Successfully found '{name}' cookie.")
                cookies[name] = value
                
                if "__Secure-1PSID" in cookies and "__Secure-1PSIDTS" in cookies:
                    break # Exit once both are found
    except Exception as e:
        print(f"An error occurred while reading the cookie database: {e}")
        cookies = None # Indicate failure
    finally:
        conn.close()
        os.remove(temp_db_path)

    if not cookies or len(cookies) < 2:
        print("\n--- FAILED ---")
        print("Could not find the required cookies (__Secure-1PSID, __Secure-1PSIDTS).")
        print("Please ensure you are logged into https://gemini.google.com/ in Firefox.")
        print("If Firefox is open, please close it completely and try again.")
        print("--------------\n")
        return None

    return cookies

def save_cookies_to_env(cookies):
    """
    Saves or updates the extracted cookies in the .env file.
    """
    if not cookies:
        return

    dotenv_path = find_dotenv()
    if not dotenv_path:
        dotenv_path = ".env"
        open(dotenv_path, "w").close()
        print("Created a new .env file.")

    set_key(dotenv_path, "__Secure-1PSID", cookies["__Secure-1PSID"])
    set_key(dotenv_path, "__Secure-1PSIDTS", cookies["__Secure-1PSIDTS"])
    
    print("\nSuccessfully saved/updated cookies in .env file.")
    print("You can now run the main application.")

if __name__ == "__main__":
    gemini_cookies = get_firefox_cookies_manual()
    if gemini_cookies:
        save_cookies_to_env(gemini_cookies)
    else:
        exit(1)
