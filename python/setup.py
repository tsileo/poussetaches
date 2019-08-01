#!/usr/bin/env python

from setuptools import setup
import io
import os


here = os.path.abspath(os.path.dirname(__file__))


# Package meta-data.
NAME = "poussetaches"
DESCRIPTION = (
    "Client for poussetaches."
)
URL = "https://github.com/tsileo/poussetaches"
EMAIL = "t@a4.io"
AUTHOR = "Thomas Sileo"
REQUIRES_PYTHON = ">=3.7.0"
VERSION = "0.1.0"


REQUIRED = [
    "requests",
    "flask",
]

DEPENDENCY_LINKS = []


# Import the README and use it as the long-description.
with io.open(os.path.join(here, "README.md"), encoding="utf-8") as f:
    long_description = "\n" + f.read()

setup(
    name=NAME,
    version=VERSION,
    description=DESCRIPTION,
    long_description=long_description,
    long_description_content_type="text/markdown",
    author=AUTHOR,
    author_email=EMAIL,
    python_requires=REQUIRES_PYTHON,
    setup_requires=['wheel'],
    url=URL,
    py_modules=["poussetaches"],
    install_requires=REQUIRED,
    dependency_links=DEPENDENCY_LINKS,
    license="ISC",
    classifiers=[
        # Trove classifiers
        # Full list: https://pypi.python.org/pypi?%3Aaction=list_classifiers
        "Development Status :: 3 - Alpha",
        "License :: OSI Approved :: ISC License (ISCL)",
        "Programming Language :: Python",
        "Programming Language :: Python :: 3.7",
        "Programming Language :: Python :: Implementation :: CPython",
        "Programming Language :: Python :: Implementation :: PyPy",
    ],
)
