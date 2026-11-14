FROM culler-project-base:2561930483d6f7f04afc49a10d2b534ba5bc75eacb810c267123288d1116edc6

# Repository shell tests run tools with PATH limited to /usr/bin:/bin, where macOS and Ubuntu install python3 and git.
RUN ln -s /usr/local/bin/python3 /usr/bin/python3 \
 && ln -s /usr/local/bin/git /usr/bin/git
