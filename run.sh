#!/bin/bash
# Change directory to the script's location
cd "$(dirname "$0")"

echo "Creating virtual environment..."
python3 -m venv .venv

echo "Activating virtual environment..."
source .venv/bin/activate

echo "Installing the gemini-webapi package in editable mode first..."
pip3 install -e .

echo "Installing dependencies from requirements.txt..."
pip3 install -r requirements.txt

echo ""
echo "Running cookie retrieval script..."
python3 get_cookies.py
echo ""

echo "=================================================="
echo "Setup complete."
echo ""
echo "Your API keys should be in a file named .env"
echo "To run the server in the future, just run this script again."
echo "=================================================="
echo ""
echo "Starting server now..."
python3 openai_adapter.py