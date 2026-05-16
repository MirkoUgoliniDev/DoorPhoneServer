#!/usr/bin/env python3
"""
DoorPhoneServer Setup Wizard — compatibility shim.

Questo file reindirizza all'entry point modulare setup/wizard.py.
Uso preferito: python3 setup/wizard.py
"""
import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

from wizard import main

if __name__ == "__main__":
    main()
