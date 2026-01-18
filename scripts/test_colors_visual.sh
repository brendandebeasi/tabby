#!/usr/bin/env bash

echo "=== COLOR PALETTE TEST ==="
echo ""
echo "Current color scheme:"
echo ""

echo "DEFAULT TABS (should be light gray):"
echo -e "\033[38;5;252m█████ colour252 - inactive default tabs\033[0m"
echo -e "\033[38;5;255m█████ colour255 - active default tabs\033[0m"
echo ""

echo "STUDIODOME TABS (should be red/pink):"
echo -e "\033[38;5;167m█████ colour167 - inactive SD| tabs\033[0m"
echo -e "\033[38;5;196m█████ colour196 - active SD| tabs\033[0m"
echo ""

echo "GUNPOWDER TABS (should be gray):"
echo -e "\033[38;5;240m█████ colour240 - inactive GP| tabs\033[0m"
echo -e "\033[38;5;250m█████ colour250 - active GP| tabs\033[0m"
echo ""

echo "Are these colors distinct enough? (y/n)"
read -r answer

if [ "$answer" = "n" ]; then
    echo ""
    echo "Let's try alternative colors..."
    echo ""
    echo "ALTERNATIVE SCHEME:"
    echo ""
    echo "STUDIODOME (brighter red):"
    echo -e "\033[38;5;160m█████ colour160 - darker red\033[0m"
    echo -e "\033[38;5;9m█████ colour9 - bright red\033[0m"
    echo ""
    echo "GUNPOWDER (blue-gray):"
    echo -e "\033[38;5;60m█████ colour60 - blue-gray\033[0m"
    echo -e "\033[38;5;67m█████ colour67 - lighter blue-gray\033[0m"
fi
